package doctrine

// GlobalID is the Live registry id for the global MCP instructions doctrine.
const GlobalID = "global"

// FeatureID is the Live registry id for a feature's base doctrine.
func FeatureID(key string) string { return "feature:" + key }
