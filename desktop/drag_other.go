//go:build !darwin

package main

func installNativeDrag() {}

// SetDragRegions is only needed on macOS (native window dragging); other systems use the web drag regions.
func (a *App) SetDragRegions(drag, noDrag [][4]float64) {}
