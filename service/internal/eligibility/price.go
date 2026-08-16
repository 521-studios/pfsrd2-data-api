package eligibility

import (
	"strconv"
	"strings"
)

// priceToCp parses a PF2 price display string ("65 gp", "1,065 gp", "5 sp", "3 cp")
// into copper pieces (gp=100, sp=10, cp=1). Returns nil when the string is empty or not
// a plain amount (e.g. "Varies"), so callers can leave the structured price absent
// rather than guess.
func priceToCp(s string) *int {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	fields := strings.Fields(s)
	if len(fields) != 2 {
		return nil
	}
	n, err := strconv.Atoi(strings.ReplaceAll(fields[0], ",", ""))
	if err != nil {
		return nil
	}
	mult := map[string]int{"gp": 100, "sp": 10, "cp": 1}[strings.ToLower(fields[1])]
	if mult == 0 {
		return nil
	}
	cp := n * mult
	return &cp
}
