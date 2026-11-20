package mailparse

import "encoding/json"

// describe shows a value with its pointers followed, for failure messages.
func describe(v any) string {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err.Error()
	}
	return string(out)
}
