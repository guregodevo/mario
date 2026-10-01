package utils

import (
	"errors"
	"fmt"
	"github.com/guregodevo/mario/logger"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var dateFormats = map[string]string{
	`^\d{4}-\d{2}-\d{2}$`: "2006-01-02",
	`^\d{8}$`:             "20060102",
	`^\d{4}-\d{2}$`:       "2006-01",
}

func Format(partition string, layout string) string {
	logger.Log.Debug("template", "Format %s like %s \n", partition, layout)
	partition_date, _, err := ParsePartitionString(partition)
	logger.Log.Debug("Format", "Format parse partition string %s \n", partition_date)
	if err != nil {
		return partition
	}
	return partition_date.Format(layout)
}

// Converts partition string to (date, date string "20060102")
func ParsePartitionString(partition string) (*time.Time, string, error) {
	for regex, layout := range dateFormats {
		if matched, _ := regexp.MatchString(regex, partition); matched {
			logger.Log.Debug("template", "Matched partition '%s' with regex '%s' and layout '%s'\n", partition, regex, layout)
			parsedDate, err := time.Parse(layout, partition)
			if err != nil {
				logger.Log.Error("template", "Failed to parse '%s' with layout '%s': %v\n", partition, layout, err)
				return nil, "", fmt.Errorf("Failed to parse date %s with layout %s: %v", partition, layout, err)
			}
			return &parsedDate, parsedDate.Format("20060102"), nil
		}
	}
	return nil, "", errors.New("Unsupported partition Format")
}

// ParseOffset takes an offset string and returns its individual components:
// years, months, days, hours, and whether or not to truncate the date.
// The offset string can contain '+' or '-' followed by an integer and a unit.
// Units can be 'Y' for years, 'M' for months, 'W' for weeks, 'D' for days, or 'H' for hours.
// The string can also contain '/' followed by a unit to indicate truncation to that unit.
func ParseOffset(offsetStr string) (int, int, int, int, bool, error) {
	years := 0
	months := 0
	days := 0
	hours := 0
	truncate := false

	re := regexp.MustCompile(`([+-]?[0-9]+[YMWDH])|(/[YMWDH])`)
	matches := re.FindAllString(offsetStr, -1)
	for _, match := range matches {
		if strings.HasSuffix(match, "Y") {
			val, err := strconv.Atoi(match[:len(match)-1])
			if err != nil {
				return 0, 0, 0, 0, false, err
			}
			years += val
		} else if strings.HasSuffix(match, "M") {
			val, err := strconv.Atoi(match[:len(match)-1])
			if err != nil {
				return 0, 0, 0, 0, false, err
			}
			months += val
		} else if strings.HasSuffix(match, "W") {
			val, err := strconv.Atoi(match[:len(match)-1])
			if err != nil {
				return 0, 0, 0, 0, false, err
			}
			days += val * 7
		} else if strings.HasSuffix(match, "D") {
			val, err := strconv.Atoi(match[:len(match)-1])
			if err != nil {
				return 0, 0, 0, 0, false, err
			}
			days += val
		} else if strings.HasSuffix(match, "H") {
			val, err := strconv.Atoi(match[:len(match)-1])
			if err != nil {
				return 0, 0, 0, 0, false, err
			}
			hours += val
		} else if strings.HasPrefix(match, "/") {
			truncate = true
			// Require truncation logic if needed
		}
	}

	return years, months, days, hours, truncate, nil
}

// Offset takes a date string in the Format "YYYY-MM-DD" and an offset string,
// and returns a new date string that represents the original date plus the offset.
// The offset string can contain years, months, weeks, days, or hours, as well as a truncation directive.
// See parseOffset for details of the offset string Format.
func Offset(format, dateStr, offsetStr string) (string, error) {
	_date, _, err := ParsePartitionString(dateStr)
	if err != nil {
		return "", err
	}

	years, months, days, hours, truncate, err := ParseOffset(offsetStr)
	if err != nil {
		return "", err
	}

	date := (*_date).AddDate(years, months, days)
	date = date.Add(time.Duration(hours) * time.Hour)

	if truncate {
		y, m, d := date.Date()
		switch offsetStr[len(offsetStr)-1] {
		case 'Y':
			date = time.Date(y, time.January, 1, 0, 0, 0, 0, date.Location())
		case 'M':
			date = time.Date(y, m, 1, 0, 0, 0, 0, date.Location())
		case 'D':
			date = time.Date(y, m, d, 0, 0, 0, 0, date.Location())
		case 'W':
			date = date.AddDate(0, 0, -int(date.Weekday()))
		}
	}

	return date.Format(format), nil
}

// getTablePrefix extracts the table prefix from the given table name
// Example: tableA_YYYYMMDD prefix is tableA
func GetTablePrefix(input string) string {
	re := regexp.MustCompile(`^(.*?)[^a-zA-Z0-9](YYYY|YYYYMM|YYYYMMDD|YYYYMMDDHH|\d{4,10})$`)

	matches := re.FindStringSubmatch(input)
	if len(matches) > 1 {
		return matches[1]
	}
	return input
}

// partitionToDate converts a partition string from "2023-08-01" to "20230801"
func partitionToDate(partition string) (time.Time, error) {
	return time.Parse("2006-01-02", partition)
}

// FormatPartition converts partition string 2008-08-01 to  "20080801" if given pattern is YYYYMMDD
func FormatPartition(partition, pattern string) string {
	if date, e := partitionToDate(partition); e != nil {
		logger.Log.Error("Format", "Could not parse partition '%s'", partition)
		return ""
	} else {
		return PartitionDateFormat(date, pattern)
	}
}

// GetTableID converts "tablename_YYYYMMDD" to "tablename_20230802"
func GetTableID(TableName, partition string) string {

	dateSuffix := FormatPartition(partition, "YYYYMMDD")
	if strings.Contains(TableName, "$") {
		// Check for native partitioned table pattern, <tablename>$YYYYMMDD
		nativePartitionTableName := fmt.Sprintf("%s$%s", GetTablePrefix(TableName), dateSuffix)
		return strings.Split(nativePartitionTableName, "$")[0]
	} else {
		// Check for sharded table pattern, <tablename>_YYYYMMDD
		return fmt.Sprintf("%s_%s", GetTablePrefix(TableName), dateSuffix)
	}
}

// PartitionDateFormat converts date to formatted string
func PartitionDateFormat(date time.Time, pattern string) string {
	switch pattern {
	case "YYYYMM":
		return date.Format("200601")
	case "YYYY-MM-DD":
		return date.Format("2006-01-02")
	case "YYYY-MM-DDTHH":
		return date.Format("2006-01-02T15")
	case "YYYYMMDD":
		return date.Format("20060102")
	case "YYYYMMDDHH":
		return date.Format("2006010215")
	default:
		logger.Log.Error("Format", "Could not find such pattern '%s'", pattern)
		return ""
	}
}
