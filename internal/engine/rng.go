package engine

// RNG is the deterministic source used by simulators and combat.
type RNG interface {
	NextInt(n int) int
	Snapshot() uint64
}

// DeterministicRNG is a tiny xorshift64* generator.
type DeterministicRNG struct {
	state uint64
}

func NewDeterministicRNG(seed uint64) *DeterministicRNG {
	if seed == 0 {
		seed = 0x9e3779b97f4a7c15
	}
	return &DeterministicRNG{state: seed}
}

func (r *DeterministicRNG) nextUint64() uint64 {
	x := r.state
	x ^= x >> 12
	x ^= x << 25
	x ^= x >> 27
	r.state = x
	return x * 2685821657736338717
}

func (r *DeterministicRNG) NextInt(n int) int {
	if n <= 0 {
		return 0
	}
	return int(r.nextUint64() % uint64(n))
}

func (r *DeterministicRNG) Snapshot() uint64 {
	return r.state
}
