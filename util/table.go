package util

import (
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"time"
	"v2ray-stat/backend/config"
	"v2ray-stat/common"
)

func Contains(slice []string, item string) bool {
	return slices.Contains(slice, item)
}

func AppendStats(builder *strings.Builder, content string) {
	builder.WriteString(content)
}

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
			strVal := ""
			switch v := val.(type) {
			case int64:
				if columns[i] == "created" || columns[i] == "sub_end" {
					if v == 0 {
						strVal = ""
					} else {
						strVal = time.Unix(v, 0).In(common.TimeLocation).Format("2006-01-02 15:04")
					}
				} else if Contains(trafficColumns, columns[i]) {
					switch columns[i] {
					case "Rate":
						strVal = FormatData(float64(v), "bps")
					case "Uplink", "Downlink", "Sess Up", "Sess Down":
						strVal = FormatData(float64(v), "byte")
					default:
						strVal = fmt.Sprintf("%d", v)
					}
				} else {
					strVal = fmt.Sprintf("%d", v)
				}
			case string:
				strVal = v
			case nil:
				strVal = ""
			default:
				strVal = fmt.Sprintf("%v", v)
			}
			if len(strVal) > 255 {
				cfg.Logger.Warn("Value too long in column", "column", columns[i], "length", len(strVal))
				strVal = strVal[:255]
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
