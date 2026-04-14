package constants

const(
	MAX_ACCOUNT_NONCES = 100_000
	// Beyond this window, Radix Trie would be swapped to disk to avoid memory exhaustion.
	SWAP_DISK_WINDOW = 10 * 1024 // 10MB
)