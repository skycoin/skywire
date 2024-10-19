// Package safecast provide a safe way to cast a numeric value from type A to type B,
// with overflow and underflow check.
package safecast

// All the code are generated.

//go:generate sh -c "python3 generate.py | gofmt > casts.go"
//go:generate sh -c "python3 generate_generic.py | gofmt > generics.go"
//go:generate sh -c "python3 generate_test.py | gofmt > safecast_test.go"
