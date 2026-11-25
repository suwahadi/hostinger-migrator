package logx

import "testing"

func TestRedactorIgnoresLiterals(t *testing.T) {
	r := NewRedactor()
	for _, v := range []string{"false", "NULL", "${PUSHER_KEY}", "rahasiaDB123"} {
		r.Add(v)
	}
	got := r.String("a=false b=null c=${PUSHER_KEY} d=rahasiaDB123")
	if want := "a=false b=null c=${PUSHER_KEY} d=" + Mask; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
