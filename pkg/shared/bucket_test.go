package shared

import (
	"io/fs"
	"testing"

	"github.com/picosh/pico/pkg/send/utils"
)

func TestGetProjectName(t *testing.T) {
	tests := []struct {
		name     string
		filepath string
		isDir    bool
		want     string
	}{
		// Standard cases: /project/file -> project
		{
			name:     "standard project with file",
			filepath: "/myproject/index.html",
			isDir:    false,
			want:     "myproject",
		},
		{
			name:     "nested path",
			filepath: "/myproject/subdir/file.txt",
			isDir:    false,
			want:     "myproject",
		},
		// Root-level file (the bug case): /bin -> bin
		{
			name:     "root-level file (scp file pgs.sh:/bin)",
			filepath: "/bin",
			isDir:    false,
			want:     "bin",
		},
		{
			name:     "root-level file with different name",
			filepath: "/myproject",
			isDir:    false,
			want:     "myproject",
		},
		// Directory cases
		{
			name:     "directory with no slash",
			filepath: "myproject",
			isDir:    true,
			want:     "myproject",
		},
		{
			name:     "directory at root level",
			filepath: "/myproject",
			isDir:    true,
			want:     "myproject",
		},
		// Edge cases (caught by uploader validation, not valid inputs)
		{
			name:     "empty path",
			filepath: "",
			isDir:    false,
			want:     ".",
		},
		{
			name:     "root path",
			filepath: "/",
			isDir:    false,
			want:     "/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := &utils.FileEntry{
				Filepath: tt.filepath,
				Mode:     fs.FileMode(0644),
			}
			if tt.isDir {
				entry.Mode = fs.ModeDir
			}

			got := GetProjectName(entry)
			if got != tt.want {
				t.Errorf("GetProjectName(%q, isDir=%v) = %q, want %q", tt.filepath, tt.isDir, got, tt.want)
			}
		})
	}
}
