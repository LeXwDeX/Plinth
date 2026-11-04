package modelinspect

import (
	"fmt"
	"math"
)

// vec3 is a double-precision vector. Parsing and bounding use float64 even
// though vertex attributes are decoded as float32, so accumulated node
// transforms and large coordinates keep their precision.
type vec3 [3]float64

func (a vec3) add(b vec3) vec3 { return vec3{a[0] + b[0], a[1] + b[1], a[2] + b[2]} }
func (a vec3) sub(b vec3) vec3 { return vec3{a[0] - b[0], a[1] - b[1], a[2] - b[2]} }
func (a vec3) scale(s float64) vec3 {
	return vec3{a[0] * s, a[1] * s, a[2] * s}
}

func (a vec3) dot(b vec3) float64 {
	return a[0]*b[0] + a[1]*b[1] + a[2]*b[2]
}

func (a vec3) cross(b vec3) vec3 {
	return vec3{
		a[1]*b[2] - a[2]*b[1],
		a[2]*b[0] - a[0]*b[2],
		a[0]*b[1] - a[1]*b[0],
	}
}

func (a vec3) length() float64 { return math.Sqrt(a.dot(a)) }

func (a vec3) normalized() vec3 {
	l := a.length()
	if l == 0 || math.IsNaN(l) || math.IsInf(l, 0) {
		return vec3{}
	}
	return a.scale(1 / l)
}

func (a vec3) finite() bool {
	return finite(a[0]) && finite(a[1]) && finite(a[2])
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// mat4 is a column-major 4x4 matrix, matching glTF's matrix layout.
type mat4 [16]float64

func identity4() mat4 {
	var m mat4
	m[0], m[5], m[10], m[15] = 1, 1, 1, 1
	return m
}

func (a mat4) mul(b mat4) mat4 {
	var out mat4
	for col := 0; col < 4; col++ {
		for row := 0; row < 4; row++ {
			out[col*4+row] =
				a[0*4+row]*b[col*4+0] +
					a[1*4+row]*b[col*4+1] +
					a[2*4+row]*b[col*4+2] +
					a[3*4+row]*b[col*4+3]
		}
	}
	return out
}

// transformPoint applies m to (x, y, z, 1) and performs the perspective
// divide. glTF node matrices are affine, so w is normally 1.
func (m mat4) transformPoint(x, y, z float64) vec3 {
	w := m[3]*x + m[7]*y + m[11]*z + m[15]
	if w == 0 || w == 1 {
		return vec3{
			m[0]*x + m[4]*y + m[8]*z + m[12],
			m[1]*x + m[5]*y + m[9]*z + m[13],
			m[2]*x + m[6]*y + m[10]*z + m[14],
		}
	}
	inv := 1 / w
	return vec3{
		(m[0]*x + m[4]*y + m[8]*z + m[12]) * inv,
		(m[1]*x + m[5]*y + m[9]*z + m[13]) * inv,
		(m[2]*x + m[6]*y + m[10]*z + m[14]) * inv,
	}
}

func (m mat4) finite() bool {
	for _, v := range m {
		if !finite(v) {
			return false
		}
	}
	return true
}

// mat3 is a row-major 3x3 matrix, used for the normal transform.
type mat3 [9]float64

func (m mat3) transform(v vec3) vec3 {
	return vec3{
		m[0]*v[0] + m[1]*v[1] + m[2]*v[2],
		m[3]*v[0] + m[4]*v[1] + m[5]*v[2],
		m[6]*v[0] + m[7]*v[1] + m[8]*v[2],
	}
}

// normalMatrix returns the inverse transpose of the upper-left 3x3 part of m.
// A singular matrix (for example a zero scale) falls back to the identity so
// that a malformed transform cannot produce NaNs throughout a render.
func (m mat4) normalMatrix() mat3 {
	a, b, c := m[0], m[4], m[8]
	d, e, f := m[1], m[5], m[9]
	g, h, i := m[2], m[6], m[10]
	det := a*(e*i-f*h) - b*(d*i-f*g) + c*(d*h-e*g)
	if det == 0 || !finite(det) {
		return mat3{1, 0, 0, 0, 1, 0, 0, 0, 1}
	}
	invDet := 1 / det
	// Inverse of the row-major matrix [[a b c][d e f][g h i]], then transpose.
	return mat3{
		(e*i - f*h) * invDet, (f*g - d*i) * invDet, (d*h - e*g) * invDet,
		(c*h - b*i) * invDet, (a*i - c*g) * invDet, (b*g - a*h) * invDet,
		(b*f - c*e) * invDet, (c*d - a*f) * invDet, (a*e - b*d) * invDet,
	}
}

// trsMatrix builds T * R * S with a normalized quaternion. A zero or
// non-finite quaternion is treated as identity, matching glTF's default.
func trsMatrix(t [3]float64, q [4]float64, s [3]float64) mat4 {
	x, y, z, w := q[0], q[1], q[2], q[3]
	n := math.Sqrt(x*x + y*y + z*z + w*w)
	if n == 0 || !finite(n) {
		x, y, z, w = 0, 0, 0, 1
	} else {
		x, y, z, w = x/n, y/n, z/n, w/n
	}
	x2, y2, z2 := x+x, y+y, z+z
	xx, yy, zz := x*x2, y*y2, z*z2
	xy, xz, yz := x*y2, x*z2, y*z2
	wx, wy, wz := w*x2, w*y2, w*z2

	var m mat4
	m[0] = (1 - (yy + zz)) * s[0]
	m[1] = (xy + wz) * s[0]
	m[2] = (xz - wy) * s[0]
	m[4] = (xy - wz) * s[1]
	m[5] = (1 - (xx + zz)) * s[1]
	m[6] = (yz + wx) * s[1]
	m[8] = (xz + wy) * s[2]
	m[9] = (yz - wx) * s[2]
	m[10] = (1 - (xx + yy)) * s[2]
	m[12], m[13], m[14], m[15] = t[0], t[1], t[2], 1
	return m
}

// bounds is an axis-aligned world-space box. The zero value is empty.
type bounds struct {
	min, max vec3
	valid    bool
}

func (b *bounds) add(p vec3) {
	if !p.finite() {
		return
	}
	if !b.valid {
		b.min, b.max, b.valid = p, p, true
		return
	}
	for i := 0; i < 3; i++ {
		if p[i] < b.min[i] {
			b.min[i] = p[i]
		}
		if p[i] > b.max[i] {
			b.max[i] = p[i]
		}
	}
}

func (b *bounds) union(o bounds) {
	if !o.valid {
		return
	}
	b.add(o.min)
	b.add(o.max)
}

func (b bounds) size() vec3 {
	if !b.valid {
		return vec3{}
	}
	return b.max.sub(b.min)
}

func (b bounds) center() vec3 {
	if !b.valid {
		return vec3{}
	}
	return b.min.add(b.max).scale(0.5)
}

func (b bounds) diagonal() float64 { return b.size().length() }

// wrapf is fmt.Errorf with an explicit sentinel. It exists so the call sites
// stay short while every wrapped error keeps a stable errors.Is identity.
func wrapf(sentinel error, format string, args ...any) error {
	return fmt.Errorf("%w: %s", sentinel, fmt.Sprintf(format, args...))
}
