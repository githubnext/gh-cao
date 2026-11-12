package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/spf13/cobra"
)

type ncRecord = map[string]any

type ncData struct {
	campaigns, workflows, repositories, runs, audits, tools, domains, issues []map[string]any
}

func nativeComputation(cmd *cobra.Command) (resultErr error) {
	flag := func(name string) string { value, _ := cmd.Flags().GetString(name); return value }
	name, campaign := cmd.Name(), flag("campaign")
	diagnose, _ := cmd.Flags().GetBool("diagnose")
	if name != "runtime-health" && name != "intelligence" {
		return nativeErrorf("Unknown computation: %s", name)
	}
	if diagnose && campaign == "" {
		return fmt.Errorf("--diagnose requires --campaign SLUG")
	}
	if diagnose && name != "runtime-health" {
		return fmt.Errorf("--diagnose is supported only for the runtime-health computation")
	}
	database := nativeSnapshotPath(cmd)
	inventory := flag("inventory")
	if inventory == "" {
		inventory = filepath.Join(".cao", "inventory-sources.json")
	}
	inventory, err := filepath.Abs(inventory)
	if err != nil {
		return err
	}
	var sources any
	if err := ncReadJSON(inventory, &sources); err != nil {
		if os.IsNotExist(err) {
			return nativeErrorf("Runtime health requires campaign inventory: %s. Run \"cao download\" first or pass --inventory FILE.", inventory)
		}
		return nativeErrorf("Unable to read computation inventory %s: %v", inventory, err)
	}
	data, err := ncInventory(sources)
	if err != nil {
		return err
	}
	db, err := openNativeSnapshot(database)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, db.Close()) }()
	for _, collection := range []struct {
		name string
		dest *[]map[string]any
	}{
		{"campaigns", &data.campaigns}, {"workflows", &data.workflows},
		{"repositories", &data.repositories}, {"runs", &data.runs},
	} {
		retained, err := readNativeCollection(cmd.Context(), db, collection.name)
		if err != nil {
			return err
		}
		*collection.dest = ncMerge(retained, *collection.dest)
	}
	if len(data.campaigns) == 0 {
		return nativeErrorf("Runtime health requires canonical Campaign inventory.")
	}
	if diagnose {
		for _, collection := range []struct {
			name string
			dest *[]map[string]any
		}{{"audits", &data.audits}, {"tools", &data.tools}, {"domains", &data.domains}, {"issues", &data.issues}} {
			*collection.dest, err = readNativeCollection(cmd.Context(), db, collection.name)
			if err != nil {
				return err
			}
		}
	}
	var previous, feedback any
	for _, input := range []struct {
		flag, label string
		dest        *any
	}{{"previous", "previous intelligence result", &previous}, {"feedback", "intelligence feedback", &feedback}} {
		if path := flag(input.flag); path != "" {
			if name != "intelligence" {
				return fmt.Errorf("--%s is supported only for the intelligence computation", input.flag)
			}
			path, err = filepath.Abs(path)
			if err != nil {
				return err
			}
			if err := ncReadJSON(path, input.dest); err != nil {
				return nativeErrorf("Unable to read %s %s: %v", input.label, path, err)
			}
		}
	}
	result, err := ncRuntimeHealth(data, campaign)
	if err != nil {
		return err
	}
	if name == "intelligence" {
		contracts, err := ncCampaignContracts(data.campaigns, data.workflows)
		if err != nil {
			return err
		}
		result, err = ncIntelligence(result, contracts, previous, feedback)
		if err != nil {
			return err
		}
	} else if diagnose {
		result["diagnostics"] = ncDiagnostics(result, data)
	}
	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(ncRecord{"command": "computation", "computation": name, "result": result})
}

func ncReadJSON(path string, value any) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(content, value)
}

func ncObject(value any) ncRecord {
	result, _ := value.(map[string]any)
	return result
}

func ncArray(value any) []any {
	if rows, ok := value.([]any); ok {
		return rows
	}
	if rows, ok := value.([]map[string]any); ok {
		values := make([]any, len(rows))
		for i := range rows {
			values[i] = rows[i]
		}
		return values
	}
	if rows, ok := value.([]string); ok {
		values := make([]any, len(rows))
		for i := range rows {
			values[i] = rows[i]
		}
		return values
	}
	return nil
}

func ncRows(value any) []map[string]any {
	result := []map[string]any{}
	for _, row := range ncArray(value) {
		if record := ncObject(row); record != nil {
			result = append(result, record)
		}
	}
	return result
}

func ncString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case float64:
		return ncNumberString(v)
	case json.Number:
		return v.String()
	case map[string]any:
		return "[object Object]"
	case []any, []map[string]any, []string:
		parts := []string{}
		for _, item := range ncArray(v) {
			parts = append(parts, ncString(item))
		}
		return strings.Join(parts, ",")
	default:
		return fmt.Sprint(v)
	}
}

func ncOptional(value any) any {
	if value == nil || value == "" {
		return nil
	}
	return ncString(value)
}

func ncNumber(value any) float64 {
	switch v := value.(type) {
	case nil:
		return 0
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		number, _ := v.Float64()
		return number
	case string:
		if strings.TrimSpace(v) == "" {
			return 0
		}
		number, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return math.NaN()
		}
		return number
	case bool:
		if v {
			return 1
		}
		return 0
	case []any, []map[string]any, []string:
		return ncNumber(ncString(v))
	}
	return math.NaN()
}

func ncDefault(value, fallback any) any {
	if value == nil {
		return fallback
	}
	return value
}

func ncCoalesce(values ...any) any {
	for _, value := range values {
		switch value.(type) {
		case string, bool, int, int64, float64, json.Number:
			if value != "" {
				return value
			}
		}
	}
	return nil
}

func ncBool(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case float64:
		return v != 0 && !math.IsNaN(v)
	}
	return true
}

func ncCopy(record ncRecord) ncRecord {
	result := ncRecord{}
	for key, value := range record {
		result[key] = value
	}
	return result
}

func ncMerge(retained, incoming []map[string]any) []map[string]any {
	rows, indexes := []map[string]any{}, map[string]int{}
	for _, group := range [][]map[string]any{retained, incoming} {
		for _, row := range group {
			key := ncString(row["id"])
			if i, exists := indexes[key]; exists {
				merged := ncCopy(rows[i])
				for k, v := range row {
					merged[k] = v
				}
				rows[i] = merged
			} else {
				indexes[key] = len(rows)
				rows = append(rows, row)
			}
		}
	}
	return rows
}

func ncTime(value any) (string, bool) {
	text, ok := value.(string)
	if !ok {
		return "", false
	}
	for _, format := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04:05Z07:00", "2006-01-02", "2006-01", "2006", time.RFC1123, time.RFC1123Z} {
		if parsed, err := time.Parse(format, text); err == nil {
			return parsed.UTC().Format("2006-01-02T15:04:05.000Z"), true
		}
	}
	return "", false
}

func ncLatest(rows []map[string]any, canonical bool) any {
	result := ""
	for _, row := range rows {
		if stamp, ok := ncTime(row["observedAt"]); ok {
			if !canonical {
				stamp = ncString(row["observedAt"])
			}
			if stamp > result {
				result = stamp
			}
		}
	}
	if result == "" {
		return nil
	}
	return result
}

func ncUnique(values []string, limit int) []string {
	result, seen := []string{}, map[string]bool{}
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result
}

func ncNumberString(number float64) string {
	if number == 0 {
		return "0"
	}
	if math.Abs(number) >= 1e-6 && math.Abs(number) < 1e21 {
		return strconv.FormatFloat(number, 'f', -1, 64)
	}
	text := strconv.FormatFloat(number, 'e', -1, 64)
	parts := strings.Split(text, "e")
	exponent, _ := strconv.Atoi(parts[1])
	sign := ""
	if exponent >= 0 {
		sign = "+"
	}
	return parts[0] + "e" + sign + strconv.Itoa(exponent)
}

// Fingerprints use JSON.stringify's canonical-key and number representation,
// not Go's HTML-escaped JSON or its different exponent thresholds.
func ncCanonicalJSON(value any) []byte {
	var output bytes.Buffer
	var write func(any)
	write = func(value any) {
		switch v := value.(type) {
		case string:
			output.WriteByte('"')
			for _, char := range v {
				switch char {
				case '\\', '"':
					output.WriteByte('\\')
					output.WriteRune(char)
				case '\b':
					output.WriteString(`\b`)
				case '\f':
					output.WriteString(`\f`)
				case '\n':
					output.WriteString(`\n`)
				case '\r':
					output.WriteString(`\r`)
				case '\t':
					output.WriteString(`\t`)
				default:
					if char < 0x20 {
						fmt.Fprintf(&output, `\u%04x`, char)
					} else {
						output.WriteRune(char)
					}
				}
			}
			output.WriteByte('"')
		case map[string]any:
			keys := make([]string, 0, len(v))
			for key := range v {
				keys = append(keys, key)
			}
			sort.Slice(keys, func(i, j int) bool {
				a, ea := strconv.ParseUint(keys[i], 10, 32)
				b, eb := strconv.ParseUint(keys[j], 10, 32)
				ia := ea == nil && a < math.MaxUint32 && strconv.FormatUint(a, 10) == keys[i]
				ib := eb == nil && b < math.MaxUint32 && strconv.FormatUint(b, 10) == keys[j]
				if ia != ib {
					return ia
				}
				if ia {
					return a < b
				}
				left, right := utf16.Encode([]rune(keys[i])), utf16.Encode([]rune(keys[j]))
				for k := 0; k < len(left) && k < len(right); k++ {
					if left[k] != right[k] {
						return left[k] < right[k]
					}
				}
				return len(left) < len(right)
			})
			output.WriteByte('{')
			for i, key := range keys {
				if i > 0 {
					output.WriteByte(',')
				}
				write(key)
				output.WriteByte(':')
				write(v[key])
			}
			output.WriteByte('}')
		case []any, []map[string]any, []string:
			output.WriteByte('[')
			for i, item := range ncArray(v) {
				if i > 0 {
					output.WriteByte(',')
				}
				write(item)
			}
			output.WriteByte(']')
		case float64:
			output.WriteString(ncNumberString(v))
		case int:
			output.WriteString(strconv.Itoa(v))
		default:
			encoded, _ := json.Marshal(value)
			output.Write(encoded)
		}
	}
	write(value)
	return output.Bytes()
}
