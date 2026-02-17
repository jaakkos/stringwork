package collab

// escapeJSON escapes s for use inside a JSON string value.
func escapeJSON(s string) string {
	result := ""
	for _, r := range s {
		switch r {
		case '"':
			result += `\"`
		case '\\':
			result += `\\`
		case '\n':
			result += `\n`
		case '\r':
			result += `\r`
		case '\t':
			result += `\t`
		default:
			result += string(r)
		}
	}
	return result
}
