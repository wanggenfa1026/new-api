package common

import (
	"maps"

	"github.com/QuantumNous/new-api/common"
	"github.com/tidwall/gjson"
)

// ApplyParamOverrideToFormValues applies the channel parameter override to the
// text fields of a multipart request and returns the fields to send upstream.
// Single-valued fields are overridden as a flat JSON object of strings, so an
// override written for the JSON form of the same API (for example
// {"response_format":"url","n":1}) also holds for the multipart form. Fields
// with several values are forwarded unchanged. The input map is not modified:
// it belongs to the client request, which every channel attempt replays.
func ApplyParamOverrideToFormValues(values map[string][]string, info *RelayInfo) (map[string][]string, error) {
	if len(getParamOverrideMap(info)) == 0 {
		return values, nil
	}

	flat := make(map[string]string, len(values))
	result := make(map[string][]string, len(values))
	for key, fieldValues := range values {
		if len(fieldValues) == 1 {
			flat[key] = fieldValues[0]
			continue
		}
		result[key] = fieldValues
	}

	jsonData, err := common.Marshal(flat)
	if err != nil {
		return nil, err
	}
	overridden, err := ApplyParamOverrideWithRelayInfo(jsonData, info)
	if err != nil {
		return nil, err
	}

	fields := make(map[string][]string, len(flat))
	gjson.ParseBytes(overridden).ForEach(func(key, value gjson.Result) bool {
		switch value.Type {
		case gjson.Null:
		case gjson.String:
			fields[key.String()] = []string{value.String()}
		default:
			fields[key.String()] = []string{value.Raw}
		}
		return true
	})
	maps.Copy(result, fields)
	return result, nil
}
