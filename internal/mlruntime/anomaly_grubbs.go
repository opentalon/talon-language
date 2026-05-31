package mlruntime

import (
	"context"
	"fmt"
	"math"
)

// FuncAnomalyGrubbs is the planner function name for Grubbs' single-outlier
// test. Distinct from FuncAnomalyZscore so plans can opt in via
// `is anomaly using grubbs`.
const FuncAnomalyGrubbs = "anomaly_grubbs"

// DefaultGrubbsAlpha is the significance level Grubbs uses by default
// (two-tailed). 0.05 is the textbook default; tunable via Input.Params["alpha"]
// to e.g. 0.01 for stricter outlier rejection. ABC tuning of Grubbs against
// a labeled fixture would search this value, mirroring how z-score's
// threshold is tuned today.
const DefaultGrubbsAlpha = 0.05

// MinGrubbsSample is Grubbs' minimum usable sample size. Below this the
// critical value blows up and the test gives misleading p-values.
const MinGrubbsSample = 3

// GrubbsAnomaly flags rows whose value's Grubbs statistic exceeds the
// critical value at the configured α. Unlike z-score (which compares |z|
// against a hand-picked threshold like 2.5), Grubbs derives its threshold
// from the sample size — a small sample needs more standard deviations to
// constitute "significant outlier" than a large one, which is the property
// the textbook z=2.5 default gets wrong on small fixtures.
//
// Input row shape: [entity_id, value]. Params["alpha"] is optional (defaults
// to 0.05). Returns one Result per row; Value=true marks Grubbs-significant.
type GrubbsAnomaly struct{}

// NewGrubbsAnomaly returns a fresh primitive instance. Stateless.
func NewGrubbsAnomaly() *GrubbsAnomaly { return &GrubbsAnomaly{} }

// Name implements Primitive.
func (g *GrubbsAnomaly) Name() string { return FuncAnomalyGrubbs }

// Compute applies Grubbs' test row-wise: compute G = |x - mean| / stddev,
// compare to the sample-size-dependent critical value G_crit(N, α). Each
// row's Explanation records G, G_crit, and the sample size so the audit
// trail surfaces what threshold the test was actually using.
//
// When |G| > G_crit there's a stronger statistical claim than "z > 2.5":
// at α=0.05 we're rejecting the null that this value came from the same
// distribution as the rest, with 5% type-I error budget across the sample.
func (g *GrubbsAnomaly) Compute(_ context.Context, in Input) ([]Result, error) {
	if len(in.Rows) < MinGrubbsSample {
		return nil, fmt.Errorf("%w: got %d, need %d", ErrSampleTooSmall, len(in.Rows), MinGrubbsSample)
	}

	alpha := DefaultGrubbsAlpha
	if a, ok := numericParam(in.Params, "alpha"); ok && a > 0 && a < 1 {
		alpha = a
	}

	idIdx := columnIndex(in.Schema, "entity_id", 0)
	valIdx := columnIndex(in.Schema, "value", 1)

	values := make([]float64, 0, len(in.Rows))
	for _, row := range in.Rows {
		if v, ok := numericAt(row, valIdx); ok {
			values = append(values, v)
		}
	}
	if len(values) < MinGrubbsSample {
		return nil, fmt.Errorf("%w: %d numeric values among %d rows", ErrSampleTooSmall, len(values), len(in.Rows))
	}

	// Grubbs uses the sample (unbiased) variance /(n-1), not the population
	// variance /n that the z-score primitive uses. The published critical-value
	// tables assume sample stddev — using population stddev shifts G by a
	// factor of sqrt(n/(n-1)) and produces type-I error inflation at small n.
	mean, stddev := meanSampleStddev(values)
	gCrit := grubbsCritical(len(values), alpha)

	results := make([]Result, 0, len(in.Rows))
	for _, row := range in.Rows {
		val, ok := numericAt(row, valIdx)
		if !ok {
			continue
		}
		entityID, _ := intAt(row, idIdx)

		var gStat float64
		if stddev > 0 {
			gStat = math.Abs(val-mean) / stddev
		}
		flagged := stddev > 0 && gStat > gCrit

		results = append(results, Result{
			EntityID: entityID,
			Value:    flagged,
			Explanation: Explanation{
				Primitive: FuncAnomalyGrubbs,
				EntityID:  entityID,
				Inputs: map[string]any{
					"observed": val,
					"mean":     mean,
					"stddev":   stddev,
					"G":        gStat,
					"G_crit":   gCrit,
					"alpha":    alpha,
					"sample_n": len(values),
				},
				Rules: []Rule{{
					Attr:     "grubbs_G",
					Op:       ">",
					Value:    gCrit,
					Observed: gStat,
				}},
				Threshold: &Threshold{
					Method: fmt.Sprintf("grubbs_alpha_%.3f", alpha),
					Value:  gCrit,
					Sample: len(values),
				},
			},
		})
	}
	return results, nil
}

// meanSampleStddev is the (Bessel-corrected) /n-1 variant of meanStddev.
// Grubbs and any other test that compares against published critical-value
// tables wants sample stddev; z-score uses population stddev for historical
// reasons. Returns (NaN, 0) for n < 2.
func meanSampleStddev(xs []float64) (float64, float64) {
	if len(xs) < 2 {
		if len(xs) == 1 {
			return xs[0], 0
		}
		return math.NaN(), 0
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	mean := sum / float64(len(xs))
	var sqSum float64
	for _, x := range xs {
		d := x - mean
		sqSum += d * d
	}
	variance := sqSum / float64(len(xs)-1)
	return mean, math.Sqrt(variance)
}

// grubbsCritical returns G_crit for sample size n at significance level α.
//
// Closed form:
//
//	G_crit(n, α) = ((n-1) / sqrt(n)) * sqrt(t² / (n - 2 + t²))
//
// where t is the one-tailed Student's-t critical at α/(2n), df = n-2. The
// Student's t inverse is approximated via Cornish-Fisher on the normal
// quantile (G. W. Hill 1970) which is accurate to ~1% for df ≥ 5 but
// noisy at very small df. To stay aligned with published Grubbs tables
// for the two α values 99% of users pick, we override with tabulated
// values for α ∈ {0.05, 0.01}; for other α we fall back to the formula.
func grubbsCritical(n int, alpha float64) float64 {
	if n < MinGrubbsSample {
		return math.Inf(1)
	}
	if v, ok := tabulatedGrubbsCritical(n, alpha); ok {
		return v
	}
	t := studentTCritical(alpha/(2*float64(n)), n-2)
	nf := float64(n)
	return ((nf - 1) / math.Sqrt(nf)) * math.Sqrt(t*t/(nf-2+t*t))
}

// tabulatedGrubbsCritical looks up the published critical value for α ∈
// {0.05, 0.01} via linear interpolation on n. For α outside this set, or
// n outside the table range, the second return value is false.
//
// Tables from NIST/SEMATECH e-Handbook §1.3.5.17 (Grubbs); cross-checked
// against the Rorabacher 1991 tables. For n > 1000 the value barely moves,
// so we cap there and fall through to the closed-form approximation.
func tabulatedGrubbsCritical(n int, alpha float64) (float64, bool) {
	var table []struct {
		N int
		G float64
	}
	switch {
	case math.Abs(alpha-0.05) < 1e-9:
		table = grubbs005
	case math.Abs(alpha-0.01) < 1e-9:
		table = grubbs001
	default:
		return 0, false
	}
	if n < table[0].N || n > table[len(table)-1].N {
		return 0, false
	}
	// Exact hit.
	for _, row := range table {
		if row.N == n {
			return row.G, true
		}
	}
	// Linear interpolation between bracket entries.
	for i := 0; i < len(table)-1; i++ {
		if n > table[i].N && n < table[i+1].N {
			lo, hi := table[i], table[i+1]
			f := float64(n-lo.N) / float64(hi.N-lo.N)
			return lo.G + f*(hi.G-lo.G), true
		}
	}
	return 0, false
}

// grubbs005 — published two-sided G_crit values at α=0.05 for sample sizes 3..1000.
// Values from NIST/SEMATECH e-Handbook §1.3.5.17.
var grubbs005 = []struct {
	N int
	G float64
}{
	{3, 1.155}, {4, 1.481}, {5, 1.715}, {6, 1.887}, {7, 2.020},
	{8, 2.126}, {9, 2.215}, {10, 2.290}, {11, 2.355}, {12, 2.412},
	{13, 2.462}, {14, 2.507}, {15, 2.549}, {16, 2.585}, {17, 2.620},
	{18, 2.651}, {19, 2.681}, {20, 2.709}, {25, 2.822}, {30, 2.908},
	{40, 3.036}, {50, 3.128}, {60, 3.199}, {70, 3.257}, {80, 3.305},
	{90, 3.347}, {100, 3.383}, {150, 3.498}, {200, 3.610}, {500, 3.836},
	{1000, 3.957},
}

// grubbs001 — published two-sided G_crit values at α=0.01.
var grubbs001 = []struct {
	N int
	G float64
}{
	{3, 1.155}, {4, 1.496}, {5, 1.764}, {6, 1.973}, {7, 2.139},
	{8, 2.274}, {9, 2.387}, {10, 2.482}, {11, 2.564}, {12, 2.636},
	{13, 2.699}, {14, 2.755}, {15, 2.806}, {16, 2.852}, {17, 2.894},
	{18, 2.932}, {19, 2.968}, {20, 3.001}, {25, 3.135}, {30, 3.236},
	{40, 3.381}, {50, 3.483}, {60, 3.560}, {70, 3.622}, {80, 3.673},
	{90, 3.716}, {100, 3.754},
}

// studentTCritical returns the one-tailed critical value of Student's
// t-distribution at probability p with df degrees of freedom.
//
// Uses Cornish-Fisher's expansion around the normal quantile — accurate to
// ~3 decimals for df ≥ 5, looser for small df. Talon's audit trail surfaces
// the actual G and G_crit so a tenant can verify against a stats package
// if they need 5+ decimal accuracy.
func studentTCritical(p float64, df int) float64 {
	if df < 1 {
		return math.Inf(1)
	}
	z := normalCriticalUpperTail(p)
	if df == 1 {
		return math.Tan(math.Pi * (0.5 - p)) // exact for df=1
	}
	dff := float64(df)
	// Cornish-Fisher (Hill 1970) — series in 1/df.
	t := z +
		(z*z*z+z)/(4*dff) +
		(5*z*z*z*z*z+16*z*z*z+3*z)/(96*dff*dff) +
		(3*z*z*z*z*z*z*z+19*z*z*z*z*z+17*z*z*z-15*z)/(384*dff*dff*dff)
	return t
}

// normalCriticalUpperTail returns z such that P(Z > z) = p for standard
// normal Z, via Acklam's inversion of the standard normal CDF (relative
// accuracy ~1e-9). Grubbs only consumes the result to ~3 decimals, so this
// is well over what we need but cheap to compute and pure Go.
func normalCriticalUpperTail(p float64) float64 {
	return normalInverseCDF(1 - p)
}

// normalInverseCDF returns z such that Φ(z) = p. Acklam (2003).
func normalInverseCDF(p float64) float64 {
	if p <= 0 {
		return math.Inf(-1)
	}
	if p >= 1 {
		return math.Inf(1)
	}

	const (
		a1 = -3.969683028665376e+01
		a2 = 2.209460984245205e+02
		a3 = -2.759285104469687e+02
		a4 = 1.383577518672690e+02
		a5 = -3.066479806614716e+01
		a6 = 2.506628277459239e+00

		b1 = -5.447609879822406e+01
		b2 = 1.615858368580409e+02
		b3 = -1.556989798598866e+02
		b4 = 6.680131188771972e+01
		b5 = -1.328068155288572e+01

		c1 = -7.784894002430293e-03
		c2 = -3.223964580411365e-01
		c3 = -2.400758277161838e+00
		c4 = -2.549732539343734e+00
		c5 = 4.374664141464968e+00
		c6 = 2.938163982698783e+00

		d1 = 7.784695709041462e-03
		d2 = 3.224671290700398e-01
		d3 = 2.445134137142996e+00
		d4 = 3.754408661907416e+00

		pLow  = 0.02425
		pHigh = 1 - pLow
	)

	switch {
	case p < pLow:
		q := math.Sqrt(-2 * math.Log(p))
		return (((((c1*q+c2)*q+c3)*q+c4)*q+c5)*q + c6) /
			((((d1*q+d2)*q+d3)*q+d4)*q + 1)
	case p <= pHigh:
		q := p - 0.5
		r := q * q
		return (((((a1*r+a2)*r+a3)*r+a4)*r+a5)*r + a6) * q /
			(((((b1*r+b2)*r+b3)*r+b4)*r+b5)*r + 1)
	default:
		q := math.Sqrt(-2 * math.Log(1-p))
		return -(((((c1*q+c2)*q+c3)*q+c4)*q+c5)*q + c6) /
			((((d1*q+d2)*q+d3)*q+d4)*q + 1)
	}
}
