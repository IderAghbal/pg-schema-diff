package schema

import (
	"regexp"
	"strings"
)

// nameFilter is one of the most generic of filters. We can use it to filter objects by their schema name or name.
// In the future, it might be expanded to include a "type" field, e.g., to filter down to specific tables.
type nameFilter func(name SchemaQualifiedName) bool

func schemaNameFilter(schema string) nameFilter {
	return func(obj SchemaQualifiedName) bool {
		return obj.SchemaName == schema
	}
}

func notSchemaNameFilter(schema string) nameFilter {
	return func(obj SchemaQualifiedName) bool {
		return obj.SchemaName != schema
	}
}

// notNameRegexFilter excludes any object whose fully-qualified name (schema.unescaped-name) matches the regex.
// The unescaped form is used so callers can write patterns without worrying about how identifiers were quoted
// for emission — e.g., partman's daily children come back as "webhook_events_p20260427"; the pattern
// `_p[0-9]+$` matches them regardless of casing-driven quoting.
func notNameRegexFilter(re *regexp.Regexp) nameFilter {
	return func(obj SchemaQualifiedName) bool {
		fqName := obj.SchemaName + "." + unescapeIdentifier(obj.EscapedName)
		return !re.MatchString(fqName)
	}
}

// unescapeIdentifier strips the surrounding double quotes (and unescapes embedded `""`) that pgx.Identifier.Sanitize
// adds when an identifier needs quoting (mixed case, reserved word, etc.). For unquoted identifiers it is a no-op.
func unescapeIdentifier(escaped string) string {
	if len(escaped) >= 2 && escaped[0] == '"' && escaped[len(escaped)-1] == '"' {
		return strings.ReplaceAll(escaped[1:len(escaped)-1], `""`, `"`)
	}
	return escaped
}

func orNameFilter(filters ...nameFilter) nameFilter {
	return func(obj SchemaQualifiedName) bool {
		for _, filter := range filters {
			if filter(obj) {
				return true
			}
		}
		return false
	}
}

func andNameFilter(filters ...nameFilter) nameFilter {
	return func(obj SchemaQualifiedName) bool {
		if len(filters) == 0 {
			return false
		}

		for _, filter := range filters {
			if !filter(obj) {
				return false
			}
		}
		return true
	}
}

func filterSliceByName[T any](objs []T, getNameFn func(T) SchemaQualifiedName, filter nameFilter) []T {
	var filteredObjs []T
	for _, obj := range objs {
		if filter(getNameFn(obj)) {
			filteredObjs = append(filteredObjs, obj)
		}
	}
	return filteredObjs
}
