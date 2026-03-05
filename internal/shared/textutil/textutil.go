package textutil

import "strings"

func FirstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func NormalizeCRLF(input string) string {
	return strings.ReplaceAll(input, "\r\n", "\n")
}

func SplitLinesCRLF(input string) []string {
	return strings.Split(NormalizeCRLF(input), "\n")
}
