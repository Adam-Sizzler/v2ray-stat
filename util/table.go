package util

import (
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"v2ray-stat/backend/config"
)

// contains checks if an item exists in a slice.
func Contains(slice []string, item string) bool {
	return slices.Contains(slice, item)
}

// appendStats appends content to a strings.Builder.
func AppendStats(builder *strings.Builder, content string) {
	builder.WriteString(content)
}

// formatTable formats SQL query results into a table.
func FormatTable(rows *sql.Rows, trafficColumns []string, cfg *config.Config) (string, error) {
	columns, err := rows.Columns()
	if err != nil {
		cfg.Logger.Error("Failed to get column names", "error", err)
		return "", fmt.Errorf("failed to get column names: %v", err)
	}

	maxWidths := make([]int, len(columns))
	for i, col := range columns {
		maxWidths[i] = len(col)
	}

	var data [][]string
	for rows.Next() {
		values := make([]any, len(columns))
		valuePtrs := make([]any, len(columns))
		for i := range columns {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			cfg.Logger.Error("Failed to scan row", "error", err)
			return "", fmt.Errorf("failed to scan row: %v", err)
		}

		row := make([]string, len(columns))
		for i, val := range values {
			strVal := fmt.Sprintf("%v", val)
			if len(strVal) > 255 {
				cfg.Logger.Warn("Value too long in column", "column", columns[i], "length", len(strVal))
				strVal = strVal[:255]
			}
			if Contains(trafficColumns, columns[i]) {
				if numVal, ok := val.(int64); ok {
					switch columns[i] {
					case "Rate":
						strVal = FormatData(float64(numVal), "bps")
					case "Uplink", "Downlink", "Sess Up", "Sess Down":
						strVal = FormatData(float64(numVal), "byte")
					default:
						strVal = fmt.Sprintf("%d", numVal)
					}
				}
			}
			row[i] = strVal
			if len(strVal) > maxWidths[i] {
				maxWidths[i] = len(strVal)
			}
		}
		data = append(data, row)
	}

	if len(data) == 0 {
		cfg.Logger.Warn("SQL query result is empty")
	}

	var header strings.Builder
	for i, col := range columns {
		header.WriteString(fmt.Sprintf("%-*s", maxWidths[i]+2, col))
	}
	header.WriteString("\n")

	var separator strings.Builder
	for _, width := range maxWidths {
		separator.WriteString(strings.Repeat("-", width) + "  ")
	}
	separator.WriteString("\n")

	var table strings.Builder
	table.WriteString(header.String())
	table.WriteString(separator.String())
	for _, row := range data {
		for i, val := range row {
			if Contains(trafficColumns, columns[i]) {
				table.WriteString(fmt.Sprintf("%*s  ", maxWidths[i], val))
			} else {
				table.WriteString(fmt.Sprintf("%-*s", maxWidths[i]+2, val))
			}
		}
		table.WriteString("\n")
	}

	return table.String(), nil
}
