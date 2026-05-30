package main

import (
	"regexp"
	"slices"
	"strings"
)

var termRegex = regexp.MustCompile(`[\s.\-]+`)

func GetSearchResults(terms []string, query string, searchIndex *SearchIndex) ([]int, map[int]int) {
	score := map[int]int{}
	minScore := len(terms)

	for _, term := range terms {
		if _, isCommon := searchIndex.Common[term]; isCommon {
			minScore--
		}

		for searchIndexKey, searchIndexValue := range searchIndex.SearchIndex {
			if strings.HasPrefix(searchIndexKey, term) {
				for _, page := range searchIndexValue {
					score[page]++
				}
			}
		}
	}

	results := []int{}

	for page := range score {
		if page >= len(searchIndex.Info) {
			continue
		}

		pageIndex := searchIndex.Info[page].Index
		if pageIndex < 0 || pageIndex >= len(searchIndex.Pages) {
			continue
		}

		title := strings.ToLower(searchIndex.Pages[pageIndex].Title)
		summary := strings.ToLower(searchIndex.Info[page].Summary)

		if score[page] >= minScore {
			results = append(results, page)

			for _, term := range terms {
				if placement := strings.Index(title, term); placement > -1 {
					score[page] += 50

					if placement == 0 || title[placement-1] == '.' {
						score[page] += 500
					}

					if placement+len(term) == len(title) || title[placement+len(term)] == '.' {
						score[page] += 500
					}
				} else if placement := strings.Index(summary, term); placement > -1 {
					if placement < 10 {
						score[page] += 20 - placement
					} else {
						score[page] += 10
					}
				}
			}

			if title == query {
				score[page] += 10_000
			} else if placement := strings.Index(title, query); placement > -1 {
				if placement < 100 {
					score[page] += 200 - placement
				} else {
					score[page] += 100
				}
			} else if placement := strings.Index(summary, query); placement > -1 {
				if placement < 25 {
					score[page] += 50 - placement
				} else {
					score[page] += 25
				}
			}
		}
	}

	return results, score
}

func PerformSearch(query string, searchIndex *SearchIndex) []CompiledSearchResult {

	var terms []string
	for _, t := range termRegex.Split(query, -1) {
		if t != "" {
			terms = append(terms, t)
		}
	}

	combined := strings.ReplaceAll(query, " ", "")

	termResults, termScores := GetSearchResults(terms, query, searchIndex)
	combinedResults, combinedScores := GetSearchResults([]string{combined}, query, searchIndex)

	for page, score := range combinedScores {
		termScores[page] += score
	}

	results := slices.Concat(termResults, combinedResults)

	slices.SortFunc(results, func(a int, b int) int {
		if termScores[b] != termScores[a] {
			return termScores[b] - termScores[a]
		}

		titleA := strings.ToLower(searchIndex.Pages[searchIndex.Info[a].Index].Title)
		titleB := strings.ToLower(searchIndex.Pages[searchIndex.Info[b].Index].Title)
		return strings.Compare(titleA, titleB)
	})

	compiledResults := []CompiledSearchResult{}
	for _, result := range results {
		compiledResults = append(compiledResults, CompiledSearchResult{
			searchIndex.Pages[searchIndex.Info[result].Index],
			searchIndex.Info[result],
		})
	}

	for i := 0; i < len(compiledResults); i++ {
		for j := i + 1; j < len(compiledResults); j++ {
			if compiledResults[i].PageInfo.Index == compiledResults[j].PageInfo.Index {
				compiledResults = slices.Concat(compiledResults[:j], compiledResults[j+1:])
				j--
			}
		}
	}

	return compiledResults
}
