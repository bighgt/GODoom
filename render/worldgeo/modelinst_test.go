package worldgeo

import (
	"math"
	"testing"
)

func apply(m [16]float32, x, y, z float32) [3]float32 {
	return [3]float32{
		m[0]*x + m[4]*y + m[8]*z + m[12],
		m[1]*x + m[5]*y + m[9]*z + m[13],
		m[2]*x + m[6]*y + m[10]*z + m[14],
	}
}

func near(a, b [3]float32) bool {
	for i := range a {
		if math.Abs(float64(a[i]-b[i])) > 1e-3 {
			return false
		}
	}
	return true
}

// TestModelMatrix: a model's +X nose follows the thing's yaw (angle 0 =
// east, 90° = north, as Doom), scale applies per model axis, the offset
// rides the yaw, positive pitch lifts the nose, and a spin turns about the
// model's own axis.
func TestModelMatrix(t *testing.T) {
	base := ModelTransform{X: 100, Y: 200, Z: 10, Scale: [3]float64{1, 1, 1}}
	cases := []struct {
		name string
		mod  func(*ModelTransform)
		in   [3]float32
		want [3]float32
	}{
		{"identity", func(*ModelTransform) {}, [3]float32{1, 0, 0}, [3]float32{101, 200, 10}},
		{"yaw 90 turns +X to +Y", func(m *ModelTransform) { m.Yaw = math.Pi / 2 }, [3]float32{1, 0, 0}, [3]float32{100, 201, 10}},
		{"per-axis scale", func(m *ModelTransform) { m.Scale = [3]float64{2, 3, 4} }, [3]float32{1, 1, 1}, [3]float32{102, 203, 14}},
		{"offset rides yaw", func(m *ModelTransform) { m.Yaw = math.Pi / 2; m.Offset = [3]float64{5, 0, 7} }, [3]float32{0, 0, 0}, [3]float32{100, 205, 17}},
		{"pitch up lifts the nose", func(m *ModelTransform) { m.Pitch = math.Pi / 2 }, [3]float32{1, 0, 0}, [3]float32{100, 200, 11}},
		{"spin about model Z", func(m *ModelTransform) { m.SpinAxis = [3]float64{0, 0, 1}; m.Spin = math.Pi }, [3]float32{1, 0, 0}, [3]float32{99, 200, 10}},
	}
	for _, c := range cases {
		tr := base
		c.mod(&tr)
		if got := apply(ModelMatrix(tr), c.in[0], c.in[1], c.in[2]); !near(got, c.want) {
			t.Errorf("%s: %v -> %v, want %v", c.name, c.in, got, c.want)
		}
	}
}
