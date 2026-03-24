package potshelper

import "encoding/hex"

/*
You are given with two hash maps of type map[uint64][]byte (block_number -> block_hash).
We have to compare the maps and do map1 - map2.
- If key1 in map1 matches with the key in map2, then we should check the value too.
  If map[key1] == map[keyfound] (same block hash), then remove that record from MAPServer.
- Else let the key-value record stay in the map.
- At last you have to return the modified map1.
- Time complexity: O(n)

This function identifies blocks that are missing or different on the client side.
The returned map contains blocks that need to be synced.
*/
func CompareMaps(MAPServer, MAPClient map[uint64][]byte) map[uint64][]byte {
	for key, serverHash := range MAPServer {
		if clientHash, exists := MAPClient[key]; exists {
			// Compare the byte slices (block hashes)
			if bytesEqual(serverHash, clientHash) {
				// Block exists on client with same hash - no sync needed
				delete(MAPServer, key)
			}
			// If hashes don't match, keep it in MAPServer (client needs this block)
		}
		// If key doesn't exist in client map, keep it (client is missing this block)
	}
	return MAPServer
}

// bytesEqual compares two byte slices for equality
// Time complexity: O(n) where n is the length of the byte slice
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// CompareMapStrings is a helper function for testing with hex string maps
// Converts hex strings to bytes and calls CompareMaps
func CompareMapStrings(MAPServer, MAPClient map[uint64]string) map[uint64]string {
	serverBytes := make(map[uint64][]byte, len(MAPServer))
	clientBytes := make(map[uint64][]byte, len(MAPClient))

	// Convert server map
	for k, v := range MAPServer {
		if b, err := hex.DecodeString(v); err == nil {
			serverBytes[k] = b
		}
	}

	// Convert client map
	for k, v := range MAPClient {
		if b, err := hex.DecodeString(v); err == nil {
			clientBytes[k] = b
		}
	}

	// Compare
	result := CompareMaps(serverBytes, clientBytes)

	// Convert back to strings
	resultStrings := make(map[uint64]string, len(result))
	for k, v := range result {
		resultStrings[k] = hex.EncodeToString(v)
	}

	return resultStrings
}

