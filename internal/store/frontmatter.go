package store

import "time"

// GetString returns a string value from frontmatter.
func GetString(fm map[string]any, key string) string {
	if v, ok := fm[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// GetInt returns an int value from frontmatter.
func GetInt(fm map[string]any, key string) int {
	if v, ok := fm[key]; ok {
		switch n := v.(type) {
		case int:
			return n
		case float64:
			return int(n)
		case int64:
			return int(n)
		}
	}
	return 0
}

// GetBool returns a bool value from frontmatter.
func GetBool(fm map[string]any, key string) bool {
	if v, ok := fm[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

// GetStringSlice returns a string slice from frontmatter.
func GetStringSlice(fm map[string]any, key string) []string {
	if v, ok := fm[key]; ok {
		if arr, ok := v.([]any); ok {
			result := make([]string, 0, len(arr))
			for _, item := range arr {
				if s, ok := item.(string); ok {
					result = append(result, s)
				}
			}
			return result
		}
	}
	return nil
}

// GetTime returns a time.Time value from frontmatter.
func GetTime(fm map[string]any, key string) time.Time {
	if v, ok := fm[key]; ok {
		switch t := v.(type) {
		case time.Time:
			return t
		case string:
			parsed, err := time.Parse(time.RFC3339, t)
			if err == nil {
				return parsed
			}
		}
	}
	return time.Time{}
}

// SetField sets a key-value pair in frontmatter, creating the map if nil.
func SetField(fm map[string]any, key string, value any) map[string]any {
	if fm == nil {
		fm = make(map[string]any)
	}
	fm[key] = value
	return fm
}

// FormatTime formats a time for frontmatter storage.
func FormatTime(t time.Time) string {
	return t.Format(time.RFC3339)
}

// Now returns the current time formatted for frontmatter.
func Now() string {
	return FormatTime(time.Now())
}
