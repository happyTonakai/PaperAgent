package urlparse

import "testing"

func TestExtractGitHubURL(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain https",
			input: "We release code at https://github.com/owner/repo for reproducibility.",
			want:  "https://github.com/owner/repo",
		},
		{
			name:  "plain http",
			input: "see http://github.com/owner/repo",
			want:  "https://github.com/owner/repo",
		},
		{
			name:  "with .git suffix",
			input: "git clone https://github.com/owner/repo.git",
			want:  "https://github.com/owner/repo",
		},
		{
			name:  "with branch path",
			input: "Implementation: https://github.com/owner/repo/tree/main/src",
			want:  "https://github.com/owner/repo",
		},
		{
			name:  "with file path",
			input: "See https://github.com/owner/repo/blob/main/README.md",
			want:  "https://github.com/owner/repo",
		},
		{
			name:  "bare github.com",
			input: "Code is available at github.com/owner/repo.",
			want:  "https://github.com/owner/repo",
		},
		{
			name:  "case-insensitive scheme",
			input: "HTTPS://GITHUB.COM/Owner/Repo",
			want:  "https://github.com/Owner/Repo",
		},
		{
			name:  "dotted owner/repo allowed",
			input: "https://github.com/my-org/my.repo",
			want:  "https://github.com/my-org/my.repo",
		},
		{
			name:  "multiple URLs returns first",
			input: "First https://github.com/a/b and also https://github.com/c/d.",
			want:  "https://github.com/a/b",
		},
		{
			name:  "in markdown link",
			input: "Code is [here](https://github.com/owner/repo).",
			want:  "https://github.com/owner/repo",
		},
		{
			name:  "no GitHub URL",
			input: "This paper has no code release mentioned in the abstract.",
			want:  "",
		},
		{
			name:  "only mention of github (no path)",
			input: "We use github for hosting.",
			want:  "",
		},
		{
			name:  "empty input",
			input: "",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractGitHubURL(tt.input)
			if got != tt.want {
				t.Errorf("ExtractGitHubURL(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
