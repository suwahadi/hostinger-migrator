package logx

import "testing"

func TestRedactorKeepsEveryRegisteredSecret(t *testing.T) {
	r := NewRedactor()
	for _, v := range []string{"${Xy!9pass", "rahasiaDB123"} {
		r.Add(v)
	}
	got := r.String("a=${Xy!9pass d=rahasiaDB123")
	if want := "a=" + Mask + " d=" + Mask; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
