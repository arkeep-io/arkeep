package auth

import "testing"

// UseCheapPasswordHashing lowers the Argon2id cost to the minimum for the rest
// of the process. Test suites that hash and verify many passwords call it from
// TestMain: with the production cost each derivation takes seconds under the
// race detector. Hashes made before and after the call do not match each
// other. It panics outside a test binary, so a server can never run with it.
func UseCheapPasswordHashing() {
	if !testing.Testing() {
		panic("auth: UseCheapPasswordHashing called outside a test binary")
	}
	argon2Time, argon2Memory = 1, 64
}
