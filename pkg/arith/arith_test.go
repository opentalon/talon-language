package arith

import (
	"errors"
	"testing"
)

func TestBinaryIntPreserving(t *testing.T) {
	cases := []struct {
		op      string
		a, b    Num
		want    string
		isFloat bool
	}{
		{"+", Int(2), Int(3), "5", false},
		{"-", Int(2), Int(3), "-1", false},
		{"*", Int(4), Int(5), "20", false},
		{"+", Int(2), Float(0.5), "2.5", true},
		{"*", Float(1.5), Int(2), "3", true}, // 3.0 renders as "3" via %g, but IsFloat stays true
		{"/", Int(4), Int(2), "2", true},     // "/" is always float division
		{"//", Int(7), Int(2), "3", false},
		{"//", Int(-7), Int(2), "-3", false}, // truncate toward zero
		{"mod", Int(-7), Int(3), "2", false}, // sign follows divisor
		{"rem", Int(-7), Int(3), "-1", false},
		{"%", Int(-7), Int(3), "-1", false}, // alias of rem
	}
	for _, c := range cases {
		got, err := Binary(c.op, c.a, c.b)
		if err != nil {
			t.Fatalf("%s(%v,%v): %v", c.op, c.a, c.b, err)
		}
		if got.String() != c.want {
			t.Errorf("%s(%v,%v) = %q, want %q", c.op, c.a, c.b, got.String(), c.want)
		}
		if got.IsFloat() != c.isFloat {
			t.Errorf("%s(%v,%v) IsFloat = %v, want %v", c.op, c.a, c.b, got.IsFloat(), c.isFloat)
		}
	}
}

func TestBinaryErrors(t *testing.T) {
	if _, err := Binary("/", Int(1), Int(0)); !errors.Is(err, ErrDivByZero) {
		t.Errorf("/0: got %v, want ErrDivByZero", err)
	}
	if _, err := Binary("//", Int(1), Int(0)); !errors.Is(err, ErrDivByZero) {
		t.Errorf("//0: got %v, want ErrDivByZero", err)
	}
	if _, err := Binary("mod", Int(1), Int(0)); !errors.Is(err, ErrModByZero) {
		t.Errorf("mod 0: got %v, want ErrModByZero", err)
	}
	if _, err := Binary("^", Int(2), Int(3)); err == nil {
		t.Errorf("unknown op: want error")
	}
}

func TestCompare(t *testing.T) {
	if Compare(Int(2), Int(3)) != -1 {
		t.Error("2<3")
	}
	if Compare(Float(3.0), Int(3)) != 0 {
		t.Error("3.0==3")
	}
	if Compare(Float(3.5), Int(3)) != 1 {
		t.Error("3.5>3")
	}
}

func TestNegAbs(t *testing.T) {
	if got := Neg(Int(5)); got.String() != "-5" || got.IsFloat() {
		t.Errorf("Neg(5) = %v", got)
	}
	if got := Abs(Float(-2.5)); got.String() != "2.5" || !got.IsFloat() {
		t.Errorf("Abs(-2.5) = %v", got)
	}
}
