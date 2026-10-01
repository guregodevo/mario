package utils

// OrderedMap holds the map and the keys slice
type OrderedMap struct {
	keys []string
	m    map[string]interface{}
}

// NewOrderedMap creates a new ordered map
func NewOrderedMap() *OrderedMap {
	return &OrderedMap{
		keys: make([]string, 0),
		m:    make(map[string]interface{}),
	}
}

// Set adds a key-value pair to the map and preserves the order of keys
func (om *OrderedMap) Set(key string, value interface{}) {
	if _, exists := om.m[key]; !exists {
		om.keys = append(om.keys, key)
	}
	om.m[key] = value
}

// Get retrieves the value for a given key from the map
func (om *OrderedMap) Get(key string) (interface{}, bool) {
	val, exists := om.m[key]
	return val, exists
}

// Delete removes a key-value pair from the map
func (om *OrderedMap) Delete(key string) {
	if _, exists := om.m[key]; exists {
		delete(om.m, key)
		// Remove the key from the keys slice
		for i, k := range om.keys {
			if k == key {
				om.keys = append(om.keys[:i], om.keys[i+1:]...)
				break
			}
		}
	}
}

// Keys returns the keys in the order they were added
func (om *OrderedMap) Keys() []string {
	return om.keys
}

// Iterate calls the given function once for each key-value pair in the order they were added
func (om *OrderedMap) Iterate(f func(key string, value interface{})) {
	for _, key := range om.keys {
		f(key, om.m[key])
	}
}
