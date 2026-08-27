package bsp

// Traverse walks the BSP tree in front-to-back order relative to the point
// (camX, camY) — the same algorithm Doom's original renderer used to decide
// draw order: at each split, visit the side the camera is standing on
// first, then the far side. visit is called once per Leaf reached, in that
// order. A Vulkan-based renderer doesn't need this for occlusion (the depth
// buffer handles that), but it's still the right tool for level traversal,
// visibility culling, and later collision.
func Traverse(root Node, camX, camY float32, visit func(*Leaf)) {
	switch n := root.(type) {
	case *Leaf:
		visit(n)
	case *InnerNode:
		if n.Side(camX, camY) >= 0 {
			Traverse(n.Right, camX, camY, visit)
			Traverse(n.Left, camX, camY, visit)
		} else {
			Traverse(n.Left, camX, camY, visit)
			Traverse(n.Right, camX, camY, visit)
		}
	}
}
