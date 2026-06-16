package recommend

import (
	"testing"

	"github.com/happyTonakai/paperagent/internal/database"
)

func TestParseExcludedKeywords(t *testing.T) {
	tests := []struct {
		name string
		prefs string
		want []string
	}{
		{
			name: "no section",
			prefs: `# 偏好
## 感兴趣的主题
- LLM
`,
			want: nil,
		},
		{
			name: "empty section",
			prefs: `# 偏好
## 排除关键词

## 备注
- foo
`,
			want: nil,
		},
		{
			name: "explicit none",
			prefs: `# 偏好
## 排除关键词
无
## 备注
- foo
`,
			want: nil,
		},
		{
			name: "single line comma-separated",
			prefs: `# 偏好
## 排除关键词
federated learning, blockchain, knowledge distillation
## 备注
- foo
`,
			want: []string{"federated learning", "blockchain", "knowledge distillation"},
		},
		{
			name: "bullet-prefixed line tolerated",
			prefs: `# 偏好
## 排除关键词
- federated learning, blockchain
`,
			want: []string{"federated learning", "blockchain"},
		},
		{
			name: "lowercased and deduped",
			prefs: `## 排除关键词
Federated Learning, blockchain, federated learning,  blockchain
`,
			want: []string{"federated learning", "blockchain"},
		},
		{
			name: "section at end of file",
			prefs: `# 偏好
## 备注
- foo

## 排除关键词
quantum computing, molecular dynamics
`,
			want: []string{"quantum computing", "molecular dynamics"},
		},
		{
			name: "blank entries dropped",
			prefs: `## 排除关键词
federated learning, , blockchain,  ,
`,
			want: []string{"federated learning", "blockchain"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseExcludedKeywords(tt.prefs)
			if !equalSlices(got, tt.want) {
				t.Errorf("ParseExcludedKeywords() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFilterArticlesByKeywords(t *testing.T) {
	abs := func(s string) *string { return &s }
	mk := func(id, title, abstract string) database.NewArticle {
		return database.NewArticle{ID: id, Title: title, Abstract: abs(abstract), Link: "https://arxiv.org/abs/" + id}
	}
	mkNoAbs := func(id, title string) database.NewArticle {
		return database.NewArticle{ID: id, Title: title, Link: "https://arxiv.org/abs/" + id}
	}

	articles := []database.NewArticle{
		mk("2401.00001", "Federated Learning with Differential Privacy", "We propose a new federated approach..."),
		mk("2401.00002", "Blockchain-based Trust Management", "A decentralized ledger for IoT."),
		mk("2401.00003", "Quantum Computing for Optimization", "Variational quantum eigensolver."),
		mk("2401.00004", "Knowledge Distillation for LLMs", "Distill a large teacher into a small student."),
		mk("2401.00005", "A Survey of Transformers", "We survey transformer architectures."),
		mkNoAbs("2401.00006", "Federated Learning at the Edge"),
	}

	tests := []struct {
		name     string
		keywords []string
		wantIDs  []string
	}{
		{
			name:     "no keywords → all kept",
			keywords: nil,
			wantIDs:  []string{"2401.00001", "2401.00002", "2401.00003", "2401.00004", "2401.00005", "2401.00006"},
		},
		{
			name:     "single keyword drops two",
			keywords: []string{"federated learning"},
			wantIDs:  []string{"2401.00002", "2401.00003", "2401.00004", "2401.00005"},
		},
		{
			name:     "case-insensitive",
			keywords: []string{"BLOCKCHAIN"},
			wantIDs:  []string{"2401.00001", "2401.00003", "2401.00004", "2401.00005", "2401.00006"},
		},
		{
			name:     "matches abstract only (no title hit)",
			keywords: []string{"decentralized ledger"},
			wantIDs:  []string{"2401.00001", "2401.00003", "2401.00004", "2401.00005", "2401.00006"},
		},
		{
			name:     "matches title only (no abstract)",
			keywords: []string{"federated learning"},
			wantIDs:  []string{"2401.00002", "2401.00003", "2401.00004", "2401.00005"},
		},
		{
			name:     "multiple keywords",
			keywords: []string{"federated learning", "quantum computing", "knowledge distillation"},
			wantIDs:  []string{"2401.00002", "2401.00005"},
		},
		{
			name:     "empty/whitespace keywords ignored",
			keywords: []string{"", "  "},
			wantIDs:  []string{"2401.00001", "2401.00002", "2401.00003", "2401.00004", "2401.00005", "2401.00006"},
		},
		{
			name:     "broad keyword drops many (intentional behaviour)",
			keywords: []string{"transformer"},
			wantIDs:  []string{"2401.00001", "2401.00002", "2401.00003", "2401.00004", "2401.00006"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FilterArticlesByKeywords(articles, tt.keywords)
			gotIDs := make([]string, 0, len(got))
			for _, a := range got {
				gotIDs = append(gotIDs, a.ID)
			}
			if !equalSlices(gotIDs, tt.wantIDs) {
				t.Errorf("FilterArticlesByKeywords() ids = %v, want %v", gotIDs, tt.wantIDs)
			}
		})
	}
}

func TestFilterArticlesByKeywords_EmptyInput(t *testing.T) {
	if got := FilterArticlesByKeywords(nil, []string{"x"}); got != nil {
		t.Errorf("nil input should return nil, got %v", got)
	}
	abs := func(s string) *string { return &s }
	if got := FilterArticlesByKeywords([]database.NewArticle{{ID: "1", Title: "t", Abstract: abs("a")}}, nil); len(got) != 1 {
		t.Errorf("nil keywords should keep all articles, got %d", len(got))
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
