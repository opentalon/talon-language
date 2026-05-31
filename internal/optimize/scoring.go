package optimize

// BinaryF1 computes the harmonic mean of precision and recall for a binary
// classifier's predicted-positive set against the labeled-positive set.
//
//	precision = TP / (TP + FP)  — of the things we flagged, how many were real
//	recall    = TP / (TP + FN)  — of the real positives, how many did we catch
//	F1        = 2 · precision · recall / (precision + recall)
//
// Returns 0 when there are no true positives (precision and recall both 0).
// Used as the ABC fitness function when tuning ML primitive thresholds
// against labeled fixtures.
func BinaryF1(predicted, actual map[int]bool) float64 {
	var tp, fp, fn int
	for id := range predicted {
		if actual[id] {
			tp++
		} else {
			fp++
		}
	}
	for id := range actual {
		if !predicted[id] {
			fn++
		}
	}
	if tp == 0 {
		return 0
	}
	precision := float64(tp) / float64(tp+fp)
	recall := float64(tp) / float64(tp+fn)
	return 2 * precision * recall / (precision + recall)
}

// BinaryPrecisionRecall returns precision and recall as a (P, R) tuple. Useful
// when a Decision wants to surface both numbers alongside F1 for transparency
// (e.g. "F1=0.93, precision=0.95, recall=0.91 — we missed 1 of 11 real positives").
func BinaryPrecisionRecall(predicted, actual map[int]bool) (float64, float64) {
	var tp, fp, fn int
	for id := range predicted {
		if actual[id] {
			tp++
		} else {
			fp++
		}
	}
	for id := range actual {
		if !predicted[id] {
			fn++
		}
	}
	if tp == 0 {
		return 0, 0
	}
	p := float64(tp) / float64(tp+fp)
	r := float64(tp) / float64(tp+fn)
	return p, r
}
