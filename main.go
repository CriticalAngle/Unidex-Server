package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	md "github.com/JohannesKaufmann/html-to-markdown"
	"github.com/JohannesKaufmann/html-to-markdown/plugin"
	"github.com/PuerkitoBio/goquery"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

var classesToRemove = []string{
	"breadcrumbs",
	"footer-wrapper",
	"tooltipMoreInfoLink",
	"nextprev",
	"tooltiptext",
	"switch-link",
	"search-words",
	"scrollToFeedback",
	"suggest",
	"page-history",
	"page-edit",
}

type PageResult struct {
	Markdown         string
	Toc              string
	SwitchButtonLink string
}

type TableOfContentsCache struct {
	value     string
	timestamp int64
}

type Page struct {
	Url   string `json:"url"`
	Title string `json:"title"`
}

func (page *Page) UnmarshalJSON(data []byte) error {
	var raw [2]json.RawMessage

	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	if err := json.Unmarshal(raw[0], &page.Url); err != nil {
		return err
	}

	if err := json.Unmarshal(raw[1], &page.Title); err != nil {
		return err
	}

	return nil
}

type PageInfo struct {
	Summary string `json:"summary"`
	Index   int    `json:"index"`
}

func (pageInfo *PageInfo) UnmarshalJSON(data []byte) error {
	var raw [2]json.RawMessage

	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	if err := json.Unmarshal(raw[0], &pageInfo.Summary); err != nil {
		return err
	}

	if err := json.Unmarshal(raw[1], &pageInfo.Index); err != nil {
		return err
	}

	return nil
}

type SearchIndex struct {
	Pages       []Page           `json:"pages"`
	Info        []PageInfo       `json:"info"`
	Common      map[string]int   `json:"common"`
	SearchIndex map[string][]int `json:"searchIndex"`
}

type CompiledSearchResult struct {
	Page
	PageInfo
}

const MaxQueryLength = 50

func main() {
	tableOfContentsCacheMap := sync.Map{}
	indexCacheMap := sync.Map{}

	router := gin.Default()
	router.Use(cors.New(cors.Config{
		AllowOrigins: []string{"https://unidexdocs.com", "http://localhost:3000"},
		AllowMethods: []string{"GET"},
		AllowHeaders: []string{"Origin", "Content-Type"},
	}))
	router.SetTrustedProxies(nil)

	client := http.Client{Timeout: 10 * time.Second}

	converter := md.NewConverter("", true, nil)
	converter.Use(plugin.GitHubFlavored())

	router.GET("/*path", func(ctx *gin.Context) {
		pagePath := ctx.Param("path")

		if pagePath == "/search" {
			searchType := ctx.Query("type")
			version := ctx.Query("version")

			query := strings.TrimSpace(strings.ToLower(ctx.Query("query")))
			if len(query) > MaxQueryLength {
				query = query[0:MaxQueryLength]
			}

			if len(query) == 0 {
				ctx.String(http.StatusBadRequest, "Error: Query (`query`) query parameter is incorrect missing!")
				return
			}

			var isManual bool
			switch searchType {
			case "1":
				isManual = true
			case "2":
				isManual = false
			default:
				ctx.String(http.StatusBadRequest, "Error: Search Type (`type`) query parameter is incorrect or missing!")
				return
			}

			var pathSearchType string
			if isManual {
				pathSearchType = "Manual"
			} else {
				pathSearchType = "ScriptReference"
			}

			pathVersion := version
			if len(pathVersion) > 0 {
				pathVersion += "/"
			}

			key := fmt.Sprintf("%s_%s", pathVersion, pathSearchType)
			searchIndex, exists := indexCacheMap.Load(key)
			if !exists {

				url := fmt.Sprintf("https://docs.unity3d.com/%sDocumentation/%s/docdata/index.json", pathVersion, pathSearchType)

				request, err := http.NewRequest("GET", url, nil)
				if err != nil {
					ctx.String(http.StatusInternalServerError, err.Error())
					return
				}

				request.Header.Set("User-Agent", "Mozilla/5.0")

				response, err := client.Do(request)
				if err != nil {
					ctx.String(http.StatusInternalServerError, err.Error())
					return
				}

				defer response.Body.Close()

				indexBytes, err := io.ReadAll(response.Body)
				if err != nil {
					ctx.String(http.StatusInternalServerError, err.Error())
					return
				}

				var newSearchIndex SearchIndex
				if err := json.Unmarshal(indexBytes, &newSearchIndex); err != nil {
					ctx.String(http.StatusInternalServerError, err.Error())
					return
				}

				searchIndex = &newSearchIndex
				indexCacheMap.Store(key, searchIndex)
			}

			searchResults := PerformSearch(query, (searchIndex.(*SearchIndex)))
			ctx.JSON(http.StatusOK, searchResults)

			return
		}

		pageUrl := "https://docs.unity3d.com" + pagePath
		request, err := http.NewRequest("GET", pageUrl, nil)
		if err != nil {
			ctx.String(http.StatusInternalServerError, err.Error())
			return
		}

		request.Header.Set("User-Agent", "Mozilla/5.0")

		response, err := client.Do(request)
		if err != nil {
			ctx.String(http.StatusInternalServerError, err.Error())
			return
		}

		defer response.Body.Close()

		doc, err := goquery.NewDocumentFromReader(response.Body)
		if err != nil {
			ctx.String(http.StatusInternalServerError, err.Error())
			return
		}

		content := doc.Find(".content-block")

		switchButtonLink := ""

		switchButtons := content.Find(".switch-link")
		if switchButtons.Length() > 0 {
			href, exists := switchButtons.Attr("href")
			if exists {
				switchButtonLink = href
			}
		}

		for _, class := range classesToRemove {
			content.Find("." + class).Remove()
		}

		baseUrl, err := url.Parse(pageUrl)
		if err != nil {
			ctx.String(http.StatusInternalServerError, err.Error())
			return
		}

		content.Find("img").Each(func(i int, s *goquery.Selection) {
			src, exists := s.Attr("src")
			if !exists || src == "" {
				return
			}

			// skip non-HTTP-ish schemes
			if strings.HasPrefix(src, "data:") ||
				strings.HasPrefix(src, "javascript:") ||
				strings.HasPrefix(src, "mailto:") {
				return
			}

			ref, err := url.Parse(src)
			if err != nil {
				return
			}

			abs := baseUrl.ResolveReference(ref).String()
			s.SetAttr("src", abs)
		})

		html, err := content.Html()
		if err != nil {
			ctx.String(http.StatusInternalServerError, err.Error())
			return
		}

		markdown, err := converter.ConvertString(html)
		if err != nil {
			ctx.String(http.StatusInternalServerError, err.Error())
			return
		}

		re := regexp.MustCompile(".+(ScriptReference|Manual)")
		tocPath := re.FindString(pagePath) + "/docdata/toc.json"

		toc, exists := tableOfContentsCacheMap.Load(tocPath)
		if !exists || time.Since(time.Unix(toc.(TableOfContentsCache).timestamp, 0)) > time.Duration(time.Hour*24) {
			request, err := http.NewRequest("GET", "https://docs.unity3d.com"+tocPath, nil)
			if err != nil {
				ctx.String(http.StatusInternalServerError, err.Error())
				return
			}

			request.Header.Set("User-Agent", "Mozilla/5.0")

			result, err := client.Do(request)
			if err != nil {
				ctx.String(http.StatusInternalServerError, err.Error())
				return
			}

			defer result.Body.Close()

			tocBytes, err := io.ReadAll(result.Body)
			if err != nil {
				ctx.String(http.StatusInternalServerError, err.Error())
				return
			}

			toc = TableOfContentsCache{
				string(tocBytes),
				time.Now().Unix(),
			}

			tableOfContentsCacheMap.Store(tocPath, toc)
		}

		pageResult := PageResult{
			markdown,
			toc.(TableOfContentsCache).value,
			switchButtonLink,
		}

		ctx.JSON(http.StatusOK, pageResult)
	})

	router.Run(":8080")
}
