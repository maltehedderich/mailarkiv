//go:build !faults

// Package fault marks the points in capture and recovery where tests stop the
// process or inject an operating system error. In a normal build, At does
// nothing, so what ships can't be made to fail this way.
package fault

// At always returns nil in a normal build.
func At(point string) error { return nil }
