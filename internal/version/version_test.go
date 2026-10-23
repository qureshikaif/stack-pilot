package version

import (
	"errors"
	"testing"
)

func TestSatisfies(t *testing.T) {
	tests := []struct {
		have, constraint string
		want             bool
	}{
		{"v22.14.0", "22", true},
		{"v22.14.0", "20", false},
		{"v22.14.0", "v22.14.0", true},
		{"v22.14.1", "22.14.0", false},
		{"v22.14.0", ">=20", true},
		{"v18.19.0", ">=20", false},
		{"v22.14.0", ">= 20 <23", true},
		{"v23.0.0", ">=20 <23", false},
		{"v20.11.1", "^20.1.0", true},
		{"v21.0.0", "^20.1.0", false},
		{"0.2.5", "^0.2.3", true},
		{"0.3.0", "^0.2.3", false},
		{"v18.0.0", "^18 || ^20", true},
		{"v19.0.0", "^18 || ^20", false},
		{"v20.4.0", "20.x", true},
		{"v20.4.0", "~20.4.1", false},
		{"v20.4.9", "~20.4.1", true},
		{"Python 3.12.4", ">=3.11", true},
		{"Python 3.10.0", ">=3.11", false},
		{"Python 3.12.4", ">=3.11,<3.13", true},
		{"Python 3.13.0", ">=3.11,<3.13", false},
		{"Python 3.12.4", "==3.12.*", true},
		{"Python 3.11.9", "==3.12.*", false},
		{"Python 3.12.4", "~=3.11", true},
		{"Python 4.0.0", "~=3.11", false},
		{"Python 3.11.9", "~=3.11.2", true},
		{"Python 3.12.0", "~=3.11.2", false},
		{"Python 3.12.4", "!=3.12.*", false},
		{"Python 3.12.4", "<=3.12", false}, // PEP 440: <=3.12 means <=3.12.0
		{"Python 3.12.0", "<=3.12", true},
		{"9.1.0", "*", true},
	}
	for _, tt := range tests {
		got, err := Satisfies(tt.have, tt.constraint)
		if err != nil {
			t.Errorf("Satisfies(%q, %q) error: %v", tt.have, tt.constraint, err)
			continue
		}
		if got != tt.want {
			t.Errorf("Satisfies(%q, %q) = %v, want %v", tt.have, tt.constraint, got, tt.want)
		}
	}
}

func TestSatisfiesUnsupported(t *testing.T) {
	for _, c := range []string{"lts/*", "lts/iron", "node", "pypy3.10"} {
		if _, err := Satisfies("v22.0.0", c); !errors.Is(err, ErrUnsupported) {
			t.Errorf("Satisfies(_, %q) err = %v, want ErrUnsupported", c, err)
		}
	}
}

func TestExtract(t *testing.T) {
	tests := map[string]string{
		"git version 2.39.3 (Apple Git-145)": "2.39.3",
		"Poetry (version 1.8.2)":             "1.8.2",
		"v22.14.0\n":                         "22.14.0",
		"uv 0.4.18 (7b55e9790 2024-10-01)":   "0.4.18",
	}
	for in, want := range tests {
		if got := Extract(in); got != want {
			t.Errorf("Extract(%q) = %q, want %q", in, got, want)
		}
	}
}
