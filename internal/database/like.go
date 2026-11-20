package database

import "strings"

// likeReplacer neutralises SQL LIKE wildcard characters.
var likeReplacer = strings.NewReplacer(
	`\`,
	`\\`,
	`%`,
	`\%`,
	`_`,
	`\_`,
)

// EscapeLike neutralises the LIKE metacharacters of a user-supplied substring.
//
// The escape character is the backslash declared by the callers' ESCAPE clause,
// so a backslash in the input has to be escaped first; doing it in the other
// order would double-escape the metacharacters added for % and _.
//
// Without it a keyword of "%" matches every row, and "_" silently turns into a
// single-character wildcard. Callers must pair this with ESCAPE '\'.
func EscapeLike(value string) string {
	return likeReplacer.Replace(value)
}
