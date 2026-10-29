package app

// Tests see a roomy disk: the free space of the machine running them must not decide whether uploads pass.
// Tests of the host floor inject their own probe through storageGate.diskSpace.
func init() {
	probeHostDisk = func(string) (uint64, uint64, error) { return 500 << 30, 1000 << 30, nil }
}
