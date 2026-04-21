package main

import (
	"testing"
)

func TestSanitizeArchiveEntryPath(t *testing.T) {
	tests := []struct {
		name        string
		entryName   string
		wantCleaned string
		wantErr     bool
	}{
		{
			name:        "simple path",
			entryName:   "test.txt",
			wantCleaned: "test.txt",
			wantErr:     false,
		},
		{
			name:        "nested path",
			entryName:   "subdir/file.txt",
			wantCleaned: "subdir/file.txt",
			wantErr:     false,
		},
		{
			name:        "windows backslash separator",
			entryName:   `subdir\file.txt`,
			wantCleaned: "subdir/file.txt",
			wantErr:     false,
		},
		{
			name:        "current directory segment",
			entryName:   "./test.txt",
			wantCleaned: "test.txt",
			wantErr:     false,
		},
		{
			name:        "parent directory escape",
			entryName:   "../secret.txt",
			wantCleaned: "",
			wantErr:     true,
		},
		{
			name:        "deep parent directory escape",
			entryName:   "../../etc/passwd",
			wantCleaned: "",
			wantErr:     true,
		},
		{
			name:        "middle segment with dot",
			entryName:   "subdir/./file.txt",
			wantCleaned: "subdir/file.txt",
			wantErr:     false,
		},
		{
			name:        "double dot only",
			entryName:   "..",
			wantCleaned: "",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := sanitizeArchiveEntryPath(tt.entryName)
			if (err != nil) != tt.wantErr {
				t.Errorf("sanitizeArchiveEntryPath() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.wantCleaned {
				t.Errorf("sanitizeArchiveEntryPath() = %q, want %q", got, tt.wantCleaned)
			}
		})
	}
}

func TestValidateInSandbox(t *testing.T) {
	tests := []struct {
		name        string
		projectPath string
		checkPath   string
		wantErr     bool
	}{
		{
			name:        "exact match",
			projectPath: "/home/user/project",
			checkPath:   "/home/user/project",
			wantErr:     false,
		},
		{
			name:        "nested within project",
			projectPath: "/home/user/project",
			checkPath:   "/home/user/project/src/main.go",
			wantErr:     false,
		},
		{
			name:        "outside project",
			projectPath: "/home/user/project",
			checkPath:   "/tmp/escape.txt",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateInSandbox(tt.projectPath, tt.checkPath)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateInSandbox() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
