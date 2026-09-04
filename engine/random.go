package engine

import "math/rand"

// Doom drives all gameplay randomness off a fixed 256-entry table
// (m_random.c) so demos replay bit-for-bit. This engine doesn't do demos
// (see the plan's guardrails), so a plain deterministically-seeded PRNG
// stands in — same statistical role (0..255), just not the exact vanilla
// sequence. Swapping in the real rndtable later is a self-contained change.
var prng = rand.New(rand.NewSource(0x1d20a))

// pRandom returns a pseudo-random value in [0, 255], id's P_Random.
func pRandom() int { return prng.Intn(256) }

// pRandomSpread is id's (P_Random() - P_Random()): two independent draws
// subtracted, giving a value in [-255, 255] centred on 0 — the shape every
// hitscan cone / aim jitter in this engine uses.
func pRandomSpread() int {
	a := pRandom()
	return a - pRandom()
}
