package embedded

type Base struct{}

func (b *Base) Promoted(a int, r *int) error { return nil }

type E struct{ *Base }

func (e *E) Own(a int, r *int) error { return nil }
