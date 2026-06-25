package recommend

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// VoteData holds community vote information for a single article.
type VoteData struct {
	HFUpvotes  *int // HuggingFace Papers 点赞数
	AXNetVotes *int // AlphaXiv 净投票数
}

// HuggingFace API response
type hfResponse struct {
	Upvotes int `json:"upvotes"`
}

// AlphaXiv API response
type axResponse struct {
	PublicTotalVotes int `json:"publicTotalVotes"`
}

var voteHTTPClient = &http.Client{
	Timeout: 15 * time.Second,
}

// FetchVotesForArticles fetches community vote data for a batch of articles.
// HuggingFace and AlphaXiv are queried in parallel for each article.
// Results that fail (rate limit, network error, etc.) are skipped.
func FetchVotesForArticles(ids []string) map[string]VoteData {
	results := make(map[string]VoteData)
	var mu sync.Mutex
	var wg sync.WaitGroup

	sem := make(chan struct{}, 5)                    // max 5 concurrent requests
	ticker := time.NewTicker(500 * time.Millisecond) // ~2 QPS rate limit
	defer ticker.Stop()

	for _, id := range ids {
		wg.Add(1)
		go func(arxivID string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			<-ticker.C // rate limit

			hfVotes := fetchHuggingFaceVotes(arxivID)
			axVotes := fetchAlphaXivVotes(arxivID)

			mu.Lock()
			results[arxivID] = VoteData{
				HFUpvotes:  hfVotes,
				AXNetVotes: axVotes,
			}
			mu.Unlock()
		}(id)
	}

	wg.Wait()
	return results
}

func fetchHuggingFaceVotes(arxivID string) *int {
	url := fmt.Sprintf("https://huggingface.co/api/papers/%s", arxivID)
	resp, err := voteHTTPClient.Get(url)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}

	var data hfResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return nil
	}

	return &data.Upvotes
}

func fetchAlphaXivVotes(arxivID string) *int {
	url := fmt.Sprintf("https://api.alphaxiv.org/papers/v3/%s/metrics", arxivID)
	resp, err := voteHTTPClient.Get(url)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}

	var data axResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return nil
	}

	return &data.PublicTotalVotes
}
