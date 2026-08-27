package vulkan

// This binding's generated struct wrappers pass Go strings straight through
// to C as a raw pointer into the Go string's backing array (a zero-copy
// optimization) instead of allocating a fresh null-terminated C string.
// That means every string handed to a Vk*CreateInfo field — application
// name, extension names, layer names, shader entry points — MUST already
// end in a NUL byte, or the C side will read past the end of the Go string
// looking for one. nz/nzAll exist purely to make that requirement visible
// and impossible to forget at each call site.

func nz(s string) string {
	return s + "\x00"
}

func nzAll(strs []string) []string {
	out := make([]string, len(strs))
	for i, s := range strs {
		out[i] = nz(s)
	}
	return out
}
