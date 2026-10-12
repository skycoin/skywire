package progkit

// Rect is a rectangle of cells, X and Y from 0 at the top left.
type Rect struct{ X, Y, W, H int }

// Empty reports whether r covers no cells.
func (r Rect) Empty() bool { return r.W <= 0 || r.H <= 0 }

// Contains reports whether the cell x, y is in r.
func (r Rect) Contains(x, y int) bool {
	return x >= r.X && y >= r.Y && x < r.X+r.W && y < r.Y+r.H
}

// Inset is r shrunk by n cells on every side.
func (r Rect) Inset(n int) Rect {
	return Rect{r.X + n, r.Y + n, max(r.W-2*n, 0), max(r.H-2*n, 0)}
}

// SplitTop is the first n rows of r and the rest.
func (r Rect) SplitTop(n int) (Rect, Rect) {
	n = min(max(n, 0), r.H)
	return Rect{r.X, r.Y, r.W, n}, Rect{r.X, r.Y + n, r.W, r.H - n}
}

// SplitBottom is r without its last n rows, and those rows.
func (r Rect) SplitBottom(n int) (Rect, Rect) {
	n = min(max(n, 0), r.H)
	return Rect{r.X, r.Y, r.W, r.H - n}, Rect{r.X, r.Y + r.H - n, r.W, n}
}

// SplitLeft is the first n columns of r and the rest.
func (r Rect) SplitLeft(n int) (Rect, Rect) {
	n = min(max(n, 0), r.W)
	return Rect{r.X, r.Y, n, r.H}, Rect{r.X + n, r.Y, r.W - n, r.H}
}
