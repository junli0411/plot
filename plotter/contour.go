// Copyright ©2015 The gonum Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package plotter

import (
	"image/color"
	"math"
	"sort"

	"gonum.org/v1/plot"
	"gonum.org/v1/plot/palette"
	"gonum.org/v1/plot/vg"
	"gonum.org/v1/plot/vg/draw"
)

// Contour implements the Plotter interface, drawing
// a contour plot of the values in the GridXYZ field.
type Contour struct {
	GridXYZ GridXYZ

	// Levels describes the contour heights to plot.
	Levels []float64

	// LineStyles is the set of styles for contour
	// lines. Line styles are are applied to each level
	// in order, modulo the length of LineStyles.
	LineStyles []draw.LineStyle

	// Palette is the color palette used to render
	// the heat map. If Palette is nil or has no
	// defined color, the Contour LineStyle color
	// is used.
	Palette palette.Palette

	// Underflow and Overflow are colors used to draw
	// contours outside the dynamic range defined
	// by Min and Max.
	Underflow color.Color
	Overflow  color.Color

	// Min and Max define the dynamic range of the
	// heat map.
	Min, Max float64
}

// NewContour creates as new contour plotter for the given data, using
// the provided palette. If levels is nil, contours are generated for
// the 0.01, 0.05, 0.25, 0.5, 0.75, 0.95 and 0.99 quantiles.
// If g has Min and Max methods that return a float, those returned
// values are used to set the respective Contour fields.
// If the returned Contour is used when Min is greater than Max, the
// Plot method will panic.
func NewContour(g GridXYZ, levels []float64, p palette.Palette) *Contour {
	var min, max float64
	type minMaxer interface {
		Min() float64
		Max() float64
	}
	switch g := g.(type) {
	case minMaxer:
		min, max = g.Min(), g.Max()
	default:
		min, max = math.Inf(1), math.Inf(-1)
		c, r := g.Dims()
		for i := 0; i < c; i++ {
			for j := 0; j < r; j++ {
				v := g.Z(i, j)
				if math.IsNaN(v) {
					continue
				}
				min = math.Min(min, v)
				max = math.Max(max, v)
			}
		}
	}

	if len(levels) == 0 {
		levels = quantilesR7(g, defaultQuantiles)
	}

	return &Contour{
		GridXYZ:    g,
		Levels:     levels,
		LineStyles: []draw.LineStyle{DefaultLineStyle},
		Palette:    p,
		Min:        min,
		Max:        max,
	}
}

// Default quantiles for case where levels is not explicitly set.
var defaultQuantiles = []float64{0.01, 0.05, 0.25, 0.5, 0.75, 0.95, 0.99}

// quantilesR7 returns the pth quantiles of the data in g according the the R-7 method.
// http://en.wikipedia.org/wiki/Quantile#Estimating_the_quantiles_of_a_population
func quantilesR7(g GridXYZ, p []float64) []float64 {
	c, r := g.Dims()
	data := make([]float64, 0, c*r)
	for i := 0; i < c; i++ {
		for j := 0; j < r; j++ {
			if v := g.Z(i, j); !math.IsNaN(v) {
				data = append(data, v)
			}
		}
	}
	sort.Float64s(data)
	v := make([]float64, len(p))
	for j, q := range p {
		if q == 1 {
			v[j] = data[len(data)-1]
		}
		h := float64(len(data)-1) * q
		i := int(h)
		v[j] = data[i] + (h-math.Floor(h))*(data[i+1]-data[i])
	}
	return v
}

// naive is a debugging constant. If true, Plot performs no contour path
// reconstruction, instead rendering each path segment individually.
const naive = false

// Plot implements the Plot method of the plot.Plotter interface.
func (h *Contour) Plot(c draw.Canvas, plt *plot.Plot) {
	if h.Min > h.Max {
		panic("contour: negative Z range")
	}

	if naive {
		h.naivePlot(c, plt)
		return
	}

	var pal []color.Color
	if h.Palette != nil {
		pal = h.Palette.Colors()
	}

	trX, trY := plt.Transforms(&c)

	// Collate contour paths and draw them.
	//
	// The alternative naive approach is to draw each line segment as
	// conrec returns it. The integrated path approach allows graphical
	// optimisations and is necessary for contour fill shading.
	cp := contourPaths(h.GridXYZ, h.Levels, trX, trY)

	// ps is a palette scaling factor to scale the palette uniformly
	// across the given levels. This enables a discordance between the
	// number of colours and the number of levels. Sorting is not
	// necessary since contourPaths sorts the levels as a side effect.
	ps := float64(len(pal)-1) / (h.Levels[len(h.Levels)-1] - h.Levels[0])
	if len(h.Levels) == 1 {
		ps = 0
	}

	for i, z := range h.Levels {
		if math.IsNaN(z) {
			continue
		}
		for _, pa := range cp[z] {
			if isLoop(pa) {
				pa.Close()
			}

			style := h.LineStyles[i%len(h.LineStyles)]
			var col color.Color
			switch {
			case z < h.Min:
				col = h.Underflow
			case z > h.Max:
				col = h.Overflow
			case len(pal) == 0:
				col = style.Color
			default:
				col = pal[int((z-h.Levels[0])*ps+0.5)] // Apply palette scaling.
			}
			if col != nil && style.Width != 0 {
				c.SetLineStyle(style)
				c.SetColor(col)
				c.Stroke(pa)
			}
		}
	}
}

// naivePlot implements the a naive rendering approach for contours.
// It is here as a debugging mode since it simply draws line segments
// generated by conrec without further computation.
func (h *Contour) naivePlot(c draw.Canvas, plt *plot.Plot) {
	var pal []color.Color
	if h.Palette != nil {
		pal = h.Palette.Colors()
	}

	trX, trY := plt.Transforms(&c)

	// Sort levels prior to palette scaling since we can't depend on
	// sorting as a side effect from calling contourPaths.
	sort.Float64s(h.Levels)
	// ps is a palette scaling factor to scale the palette uniformly
	// across the given levels. This enables a discordance between the
	// number of colours and the number of levels.
	ps := float64(len(pal)-1) / (h.Levels[len(h.Levels)-1] - h.Levels[0])
	if len(h.Levels) == 1 {
		ps = 0
	}

	levelMap := make(map[float64]int)
	for i, z := range h.Levels {
		levelMap[z] = i
	}

	// Draw each line segment as conrec generates it.
	var pa vg.Path
	conrec(h.GridXYZ, h.Levels, func(_, _ int, l line, z float64) {
		if math.IsNaN(z) {
			return
		}

		pa = pa[:0]

		x1, y1 := trX(l.p1.X), trY(l.p1.Y)
		x2, y2 := trX(l.p2.X), trY(l.p2.Y)

		pt1 := vg.Point{X: x1, Y: y1}
		pt2 := vg.Point{X: x2, Y: y2}
		if !c.Contains(pt1) || !c.Contains(pt2) {
			return
		}

		pa.Move(pt1)
		pa.Line(pt2)
		pa.Close()

		style := h.LineStyles[levelMap[z]%len(h.LineStyles)]
		var col color.Color
		switch {
		case z < h.Min:
			col = h.Underflow
		case z > h.Max:
			col = h.Overflow
		case len(pal) == 0:
			col = style.Color
		default:
			col = pal[int((z-h.Levels[0])*ps+0.5)] // Apply palette scaling.
		}
		if col != nil && style.Width != 0 {
			c.SetLineStyle(style)
			c.SetColor(col)
			c.Stroke(pa)
		}
	})
}

// DataRange implements the DataRange method
// of the plot.DataRanger interface.
func (h *Contour) DataRange() (xmin, xmax, ymin, ymax float64) {
	c, r := h.GridXYZ.Dims()
	return h.GridXYZ.X(0), h.GridXYZ.X(c - 1), h.GridXYZ.Y(0), h.GridXYZ.Y(r - 1)
}

// GlyphBoxes implements the GlyphBoxes method
// of the plot.GlyphBoxer interface.
func (h *Contour) GlyphBoxes(plt *plot.Plot) []plot.GlyphBox {
	c, r := h.GridXYZ.Dims()
	b := make([]plot.GlyphBox, 0, r*c)
	for i := 0; i < c; i++ {
		for j := 0; j < r; j++ {
			b = append(b, plot.GlyphBox{
				X: plt.X.Norm(h.GridXYZ.X(i)),
				Y: plt.Y.Norm(h.GridXYZ.Y(j)),
				Rectangle: vg.Rectangle{
					Min: vg.Point{X: -2.5, Y: -2.5},
					Max: vg.Point{X: +2.5, Y: +2.5},
				},
			})
		}
	}
	return b
}

// isLoop returns true iff a vg.Path is a closed loop.
func isLoop(p vg.Path) bool {
	s := p[0]
	e := p[len(p)-1]
	return s.Pos == e.Pos
}

// contourPaths returns a collection of vg.Paths describing contour lines based
// on the input data in m cut at the given levels. The trX and trY function
// are coordinate transforms. The returned map contains slices of paths keyed
// on the value of the contour level. contouPaths sorts levels ascending as a
// side effect.
func contourPaths(m GridXYZ, levels []float64, trX, trY func(float64) vg.Length) map[float64][]vg.Path {
	sort.Float64s(levels)

	ends := make(map[float64]endMap)
	conts := make(contourSet)
	conrec(m, levels, func(_, _ int, l line, z float64) {
		paths(l, z, ends, conts)
	})
	ends = nil

	// TODO(kortschak): Check that all non-loop paths have
	// both ends at boundary. If any end is not at a boundary
	// it may have a partner near by. Find this partner and join
	// the two conts by merging the near by ends at the mean
	// location. This operation is done level by level to ensure
	// close contours of different heights are not joined.
	// A partner should be a float error different end, but I
	// suspect that is is possible for a bi- or higher order
	// furcation so it may be that the path ends at middle node
	// of another path. This needs to be investigated.

	// Excise loops from crossed paths.
	for c := range conts {
		// Always try to do quick excision in production if possible.
		c.exciseLoops(conts, true)
	}

	// Build vg.Paths.
	paths := make(map[float64][]vg.Path)
	for c := range conts {
		paths[c.z] = append(paths[c.z], c.path(trX, trY))
	}

	return paths
}

// contourSet hold a working collection of contours.
type contourSet map[*contour]struct{}

// endMap holds a working collection of available ends.
type endMap map[point]*contour

// paths extends a conrecLine function to build a set of contours that represent
// paths along contour lines. It is used as the engine for a closure where ends
// and conts are closed around in a conrecLine function, and l and z are the
// line and height values provided by conrec. At the end of a conrec call,
// conts will contain a map keyed on the set of paths.
func paths(l line, z float64, ends map[float64]endMap, conts contourSet) {
	zEnds, ok := ends[z]
	if !ok {
		zEnds = make(endMap)
		ends[z] = zEnds
		c := newContour(l, z)
		zEnds[l.p1] = c
		zEnds[l.p2] = c
		conts[c] = struct{}{}
		return
	}

	c1, ok1 := zEnds[l.p1]
	c2, ok2 := zEnds[l.p2]

	// New segment.
	if !ok1 && !ok2 {
		c := newContour(l, z)
		zEnds[l.p1] = c
		zEnds[l.p2] = c
		conts[c] = struct{}{}
		return
	}

	if ok1 {
		// Add l.p2 to end of l.p1's contour.
		if !c1.extend(l, zEnds) {
			panic("internal link")
		}
	} else if ok2 {
		// Add l.p1 to end of l.p2's contour.
		if !c2.extend(l, zEnds) {
			panic("internal link")
		}
	}

	if c1 == c2 {
		return
	}

	// Join conts.
	if ok1 && ok2 {
		if !c1.connect(c2, zEnds) {
			panic("internal link")
		}
		delete(conts, c2)
	}
}

// path is a set of points forming a path.
type path []point

// contour holds a set of point lying sequentially along a contour line
// at height z.
type contour struct {
	z float64

	// backward and forward must each always have at least one entry.
	backward path
	forward  path
}

// newContour returns a contour starting with the end points of l for the
// height z.
func newContour(l line, z float64) *contour {
	return &contour{z: z, forward: path{l.p1}, backward: path{l.p2}}
}

func (c *contour) path(trX, trY func(float64) vg.Length) vg.Path {
	var pa vg.Path
	p := c.front()
	pa.Move(vg.Point{X: trX(p.X), Y: trY(p.Y)})
	for i := len(c.backward) - 2; i >= 0; i-- {
		p = c.backward[i]
		pa.Line(vg.Point{X: trX(p.X), Y: trY(p.Y)})
	}
	for _, p := range c.forward {
		pa.Line(vg.Point{X: trX(p.X), Y: trY(p.Y)})
	}

	return pa
}

// front returns the first point in the contour.
func (c *contour) front() point { return c.backward[len(c.backward)-1] }

// back returns the last point in the contour
func (c *contour) back() point { return c.forward[len(c.forward)-1] }

// extend adds the line l to the contour, updating the ends map. It returns
// a boolean indicating whether the extension was successful.
func (c *contour) extend(l line, ends endMap) (ok bool) {
	switch c.front() {
	case l.p1:
		c.backward = append(c.backward, l.p2)
		delete(ends, l.p1)
		ends[l.p2] = c
		return true
	case l.p2:
		c.backward = append(c.backward, l.p1)
		delete(ends, l.p2)
		ends[l.p1] = c
		return true
	}

	switch c.back() {
	case l.p1:
		c.forward = append(c.forward, l.p2)
		delete(ends, l.p1)
		ends[l.p2] = c
		return true
	case l.p2:
		c.forward = append(c.forward, l.p1)
		delete(ends, l.p2)
		ends[l.p1] = c
		return true
	}

	return false
}

// reverse reverses the order of the point in a path and returns it.
func (p path) reverse() path {
	for i, j := 0, len(p)-1; i < j; i, j = i+1, j-1 {
		p[i], p[j] = p[j], p[i]
	}
	return p
}

// connect connects the contour b with the receiver, updating the ends map.
// It returns a boolean indicating whether the connection was successful.
func (c *contour) connect(b *contour, ends endMap) (ok bool) {
	switch c.front() {
	case b.front():
		delete(ends, c.front())
		ends[b.back()] = c
		c.backward = append(c.backward, b.backward.reverse()[1:]...)
		c.backward = append(c.backward, b.forward...)
		return true
	case b.back():
		delete(ends, c.front())
		ends[b.front()] = c
		c.backward = append(c.backward, b.forward.reverse()[1:]...)
		c.backward = append(c.backward, b.backward...)
		return true
	}

	switch c.back() {
	case b.front():
		delete(ends, c.back())
		ends[b.back()] = c
		c.forward = append(c.forward, b.backward.reverse()[1:]...)
		c.forward = append(c.forward, b.forward...)
		return true
	case b.back():
		delete(ends, c.back())
		ends[b.front()] = c
		c.forward = append(c.forward, b.forward.reverse()[1:]...)
		c.forward = append(c.forward, b.backward...)
		return true
	}

	return false
}

// exciseLoops finds loops within the contour that do not include the
// start and end. Loops are removed from the contour and added to the
// contour set. Loop detection is performed by Johnson's algorithm for
// finding elementary cycles.
func (c *contour) exciseLoops(conts contourSet, quick bool) {
	if quick {
		// Find cases we can guarantee don't need
		// a complete analysis.
		seen := make(map[point]struct{})
		var crossOvers int
		for _, p := range c.backward {
			if _, ok := seen[p]; ok {
				crossOvers++
			}
			seen[p] = struct{}{}
		}
		for _, p := range c.forward[:len(c.forward)-1] {
			if _, ok := seen[p]; ok {
				crossOvers++
			}
			seen[p] = struct{}{}
		}
		switch crossOvers {
		case 0:
			return
		case 1:
			c.exciseQuick(conts)
			return
		}
	}

	wp := append(c.backward.reverse(), c.forward...)
	g := graphFrom(wp)
	cycles := cyclesIn(g)
	if len(cycles) == 0 {
		// No further work to do but clean up after ourselves.
		// We should not have reached here.
		c.backward.reverse()
		return
	}
	delete(conts, c)

	// Put loops into the contour set.
	for _, cyc := range cycles {
		loop := wp.subpath(cyc)
		conts[&contour{
			z:        c.z,
			backward: loop[:1:1],
			forward:  loop[1:],
		}] = struct{}{}
	}

	// Find non-loop paths and keep them.
	g.remove(cycles)
	paths := wp.linearPathsIn(g)
	for _, p := range paths {
		conts[&contour{
			z:        c.z,
			backward: p[:1:1],
			forward:  p[1:],
		}] = struct{}{}
	}
}

// graphFrom returns a graph representing the point path p.
func graphFrom(p path) graph {
	g := make([]set, len(p))
	seen := make(map[point]int)
	for i, v := range p {
		if _, ok := seen[v]; !ok {
			seen[v] = i
		}
	}

	for i, v := range p {
		e, ok := seen[v]
		if ok && g[e] == nil {
			g[e] = make(set)
		}
		if i < len(p)-1 {
			g[e][seen[p[i+1]]] = struct{}{}
		}
	}

	return g
}

// subpath returns a subpath given the slice of point indices
// into the path.
func (p path) subpath(i []int) path {
	pa := make(path, 0, len(i))
	for _, n := range i {
		pa = append(pa, p[n])
	}
	return pa
}

// linearPathsIn returns the linear paths in g created from p.
// If g contains any cycles linearPaths will panic.
func (p path) linearPathsIn(g graph) []path {
	var pa []path

	var u int
	for u < len(g) {
		for ; u < len(g) && len(g[u]) == 0; u++ {
		}
		if u == len(g) {
			return pa
		}
		var curr path
		for {
			if len(g[u]) == 0 {
				curr = append(curr, p[u])
				pa = append(pa, curr)
				if u == len(g)-1 {
					return pa
				}
				break
			}
			if len(g[u]) > 1 {
				panic("contour: not a linear path")
			}
			for v := range g[u] {
				curr = append(curr, p[u])
				u = v
				break
			}
		}
	}

	return pa
}

// exciseQuick is a heuristic approach to loop excision. It does not
// correctly identify loops in all cases, but those cases are likely
// to be rare.
func (c *contour) exciseQuick(conts contourSet) {
	wp := append(c.backward.reverse(), c.forward...)
	seen := make(map[point]int)
	for j := 0; j < len(wp); {
		p := wp[j]
		if i, ok := seen[p]; ok && p != wp[0] && p != wp[len(wp)-1] {
			conts[&contour{
				z:        c.z,
				backward: path{wp[i]},
				forward:  append(path(nil), wp[i+1:j+1]...),
			}] = struct{}{}
			wp = append(wp[:i], wp[j:]...)
			j = i + 1
		} else {
			seen[p] = j
			j++
		}
	}
	c.backward = c.backward[:1]
	c.backward[0] = wp[0]
	c.forward = wp[1:]
}
