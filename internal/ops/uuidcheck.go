package ops

// isCanonicalUUID reports whether s is a UUID in canonical lowercase
// 8-4-4-4-12 hex form, the only form tw generates (uuid.New().String()).
// Anything else must never reach the relay's shared Xray client list.
func isCanonicalUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
				return false
			}
		}
	}
	return true
}
