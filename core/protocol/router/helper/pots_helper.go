package helper

/*
You are given with two hash maps of type map[int]string. we have to comapre the map and do map1 - map2.
- if key1 in map1 match with the key in the map2 then we should cehck the value too if map[key1] == map[keyfound] then remove that record from the both the map.
- Else let the key value record stay in the map.
- Atlast you have to return the modified map1.
- Time complexity : O(n)
*/
func CompareMaps(MAPServer, MAPClient map[int]string) map[int]string {
	for key, value := range MAPServer {
		if clientValue, exists := MAPClient[key]; exists && clientValue == value {
			delete(MAPServer, key)
		}
	}
	return MAPServer
}
