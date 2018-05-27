package main

import (
	"fmt"
	"github.com/yhat/scrape"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"log"
	"net/http"
	"strconv"
	"strings"
)

func scrapeHN(minScore int) ([]feedItem, error) {
	res, err := http.Get("https://news.ycombinator.com/")
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	root, err := html.Parse(res.Body)
	if err != nil {
		return nil, err
	}

	athings := scrape.FindAll(root, func(n *html.Node) bool {
		return n.DataAtom == atom.Tr && scrape.Attr(n, "class") == "athing"
	})

	subtexts := scrape.FindAll(root, func(n *html.Node) bool {
		return n.DataAtom == atom.Td && scrape.Attr(n, "class") == "subtext"
	})

	log.Printf("%d athings, %d subtexts", len(athings), len(subtexts))

	items := make([]feedItem, 0, 30)
	for i, athing := range athings {
		if i >= len(subtexts) {
			log.Printf("scrapeHN: missing subtext")
			break
		}

		subtext := subtexts[i]

		scoreElem, ok := scrape.Find(subtext, func(n *html.Node) bool {
			return n.DataAtom == atom.Span && scrape.Attr(n, "class") == "score"
		})
		if !ok {
			log.Printf("scrapeHN: Missing score")
			continue
		}

		score, err := strconv.Atoi(strings.Fields(scrape.Text(scoreElem))[0])
		if err != nil {
			log.Printf("scrapeHN: Could not parse score")
			continue
		}

		if score < minScore {
			continue
		}

		titleElem, ok := scrape.Find(athing, func(n *html.Node) bool {
			return n.DataAtom == atom.A && n.Parent != nil && scrape.Attr(n.Parent, "class") == "title"
		})
		if !ok {
			log.Printf("scrapeHN: Missing title")
			continue
		}
		title := scrape.Text(titleElem)
		link := scrape.Attr(titleElem, "href")

		var domain string
		sitestrElem, ok := scrape.Find(athing, func(n *html.Node) bool {
			return n.DataAtom == atom.Span && scrape.Attr(n, "class") == "sitestr"
		})
		if ok {
			domain = scrape.Text(sitestrElem)
		}

		commentsLink, ok := scrape.Find(subtext, func(n *html.Node) bool {
			if n.DataAtom != atom.A {
				return false
			}

			text := scrape.Text(n)
			return strings.HasSuffix(text, "comment") || strings.HasSuffix(text, "comments") || strings.HasSuffix(text, "discuss")
		})
		if !ok {
			log.Printf("scrapeHN: Missing comments")
			continue
		}

		hnLink := "https://news.ycombinator.com/" + scrape.Attr(commentsLink, "href")

		items = append(items, feedItem{
			Title: fmt.Sprintf("(%s) %s", domain, title),
			GUID:  link,
			Link:  hnLink,
		})
	}

	return items, nil
}
