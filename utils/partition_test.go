package utils

import (
	"fmt"
	"testing"
	"time"

	"errors"
)

func TestFormat(t *testing.T) {
	tests := []struct {
		input    string
		format   string
		expected string
	}{
		{
			input:    "2023-10-01",
			format:   "20060102",
			expected: "20231001",
		},
		{
			input:    "2023-10-01",
			format:   "2006-01-02",
			expected: "2023-10-01",
		},
		{
			input:    "2023-10-01",
			format:   "02/01/2006",
			expected: "01/10/2023",
		},
		{
			input:    "2023-10-01",
			format:   "January 2, 2006",
			expected: "October 1, 2023",
		},
	}

	for _, test := range tests {
		actual := Format(test.input, test.format)
		if actual != test.expected {
			t.Errorf("Expected %s for input (%s) and Format (%s), but got %s", test.expected, test.input, test.format, actual)
		}
	}
}

func TestParseOffset(t *testing.T) {
	years, months, days, hours, truncate, err := ParseOffset("1Y-2M+3W+4D-5H")
	if err != nil {
		t.Fatalf("Error parsing offset: %v", err)
	}
	if years != 1 || months != -2 || days != 25 || hours != -5 || truncate != false {
		t.Fatalf("Expected 1, -2, 25, -5, false, but got %d, %d, %d, %d, %v", years, months, days, hours, truncate)
	}
}

func TestOffset(t *testing.T) {
	tests := []struct {
		format   string
		dateStr  string
		offset   string
		expected string
	}{
		{"2006-01-02", "2023-10-01", "1Y-2M+3W+4D-5H", "2024-08-25"},
		{"2006-01-02", "2023-10-01", "1Y", "2024-10-01"},
		{"2006-01-02", "2023-10-01", "-1M", "2023-09-01"},
		{"2006-01-02", "2023-10-01", "3D", "2023-10-04"},
		{"2006-01-02", "2023-10-01", "3D+1M", "2023-11-04"},
		{"2006-01-02", "2023-10-01", "+1M", "2023-11-01"},
		{"2006-01-02", "2023-10-01", "-2W", "2023-09-17"},
		{"2006-01-02", "2023-01-01", "+1Y", "2024-01-01"},
		{"2006-01-02", "2022-08-01", "1D", "2022-08-02"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s %s", tt.dateStr, tt.offset), func(t *testing.T) {
			got, err := Offset(tt.format, tt.dateStr, tt.offset)
			if err != nil {
				t.Fatalf("Error calculating offset: %v", err)
			}
			if got != tt.expected {
				t.Fatalf("Expected %s, but got %s", tt.expected, got)
			}
		})
	}
}

func TestParsePartitionString(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Time
		err      error
	}{
		{
			input:    "2023-10-01",
			expected: time.Date(2023, 10, 1, 0, 0, 0, 0, time.UTC),
			err:      nil,
		},
		{
			input:    "20231001",
			expected: time.Date(2023, 10, 1, 0, 0, 0, 0, time.UTC),
			err:      nil,
		},
		{
			input:    "2023-10",
			expected: time.Date(2023, 10, 1, 0, 0, 0, 0, time.UTC),
			err:      nil,
		},
		{
			input:    "invalid-date",
			expected: time.Time{},
			err:      errors.New("Unsupported partition Format"),
		},
	}

	for _, test := range tests {
		actual, _, err := ParsePartitionString(test.input)
		if (test.err == nil && err != nil) || (test.err != nil && err == nil) || (test.err != nil && err != nil && err.Error() != test.err.Error()) {
			t.Errorf("Expected error %v, but got %v", test.err, err)
			continue
		}

		if actual != nil && !actual.Equal(test.expected) {
			t.Errorf("Expected date %v, but got %v", test.expected, actual)
		} else if actual == nil && !test.expected.IsZero() {
			t.Errorf("Expected date %v, but got nil", test.expected)
		}
	}
}

func TestPartitionDateFormat(t *testing.T) {
	date := time.Date(2023, 10, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		format   string
		expected string
	}{
		{"YYYYMMDD", "20231001"},
		{"YYYY-MM-DD", "2023-10-01"},
		{"YYYY-MM-DDTHH", "2023-10-01T00"},
		{"YYYYMM", "202310"},
		{"YYYYMMDDHH", "2023100100"},
	}

	for _, tt := range tests {
		t.Run(tt.format, func(t *testing.T) {
			got := PartitionDateFormat(date, tt.format)
			if got != tt.expected {
				t.Fatalf("Expected %s, but got %s", tt.expected, got)
			}
		})
	}
}

func TestGetTablePrefix(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"table_A_20231001", "table_A"},
		{"table$A$20231001", "table$A"},
		{"table%A%20231001", "table%A"},
		{"table_A_YYYYMMDD", "table_A"},
		{"table#A#20231001", "table#A"}, // Invalid separator, return original string
		{"table_A", "table_A"},          // No date part, return original string
	}

	for _, tt := range tests {
		actual := GetTablePrefix(tt.input)
		if actual != tt.expected {
			t.Errorf("getTablePrefix(%q) = %q; want %q", tt.input, actual, tt.expected)
		}
	}
}

func TestFormatPartition(t *testing.T) {

	tests := []struct {
		partition string
		pattern   string
		expected  string
	}{
		{"2008-08-01", "YYYYMMDD", "20080801"},
		{"2008-08-01", "YYYYMM", "200808"},
		{"invalid-date", "YYYYMMDD", ""},
		{"2008-08-01", "invalid-pattern", ""},
		{"", "", ""},
	}

	for _, test := range tests {
		actual := FormatPartition(test.partition, test.pattern)
		if actual != test.expected {
			t.Errorf("For partition %s and pattern %s, expected %s but got %s",
				test.partition, test.pattern, test.expected, actual)
		}
	}
}
