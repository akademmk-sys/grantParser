package main

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gocolly/colly/v2"
)

type Product struct {
	PName       string
	PLink       string
	ImgLink     string
	Price       string
	PartNumber  string
	Brand       string
	Description string
}

type Category struct {
	Name     string
	Link     string
	Pages    int
	Products map[string]Product
}

func errorHandler(r *colly.Response, err error) {
	fmt.Printf("Ошибка запроса: [%d] на %s: %v\n", r.StatusCode, r.Request.URL, err)

	v := r.Request.Ctx.Get("retry_count")
	retryCount := 0
	if v != "" {
		retryCount, _ = strconv.Atoi(v)
	}
	if retryCount < 3 {
		retryCount++
		fmt.Printf("retry request (%d/3) on URL: %s\n", retryCount, r.Request.URL)
		r.Request.Ctx.Put("retry_count", strconv.Itoa(retryCount))
		r.Request.Retry()
	}
}

func main() {
	catColl := colly.NewCollector(
		colly.AllowedDomains("garant1.ru"),
		colly.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"),
	)

	catColl.Limit(&colly.LimitRule{
		DomainGlob:  "*",
		Delay:       3 * time.Second,
		RandomDelay: 1 * time.Second,
	})
	catColl.SetRequestTimeout(60 * time.Second)

	DATA := make(map[string]*Category)

	catColl.OnHTML("div.cat-tiles .cat-tile a", func(h *colly.HTMLElement) {
		name := strings.TrimSpace(h.Text)
		link := h.Request.AbsoluteURL(h.Attr("href"))
		DATA[link] = &Category{
			Name:     name,
			Link:     link,
			Pages:    1,
			Products: make(map[string]Product),
		}
	})

	var mu sync.Mutex
	tumbColl := catColl.Clone()
	tumbColl.Async = true
	tumbColl.Limit(&colly.LimitRule{
		DomainGlob:  "*",
		Delay:       3 * time.Second,
		RandomDelay: 1 * time.Second,
		Parallelism: 2,
	})

	tumbColl.OnHTML("div#table-data-pagination a.pages_n", func(h *colly.HTMLElement) {
		n, err := strconv.Atoi(h.Attr("rel"))
		if err != nil {
			return
		}

		u := h.Request.URL
		key := u.Scheme + "://" + u.Host + u.Path

		mu.Lock()
		if cat, ok := DATA[key]; ok && n > cat.Pages {
			cat.Pages = n
		}
		mu.Unlock()
	})

	tumbColl.OnHTML(`tr[itemprop="itemListElement"] td>a`, func(h *colly.HTMLElement) {
		currentURL := h.Request.URL
		baseSupCategryURl := currentURL.Scheme + "://" + currentURL.Host + currentURL.Path

		pName := strings.TrimSpace(h.Text)
		pLink := h.Request.AbsoluteURL(h.Attr("href"))
		product := Product{
			PName: pName,
			PLink: pLink,
		}
		mu.Lock()
		if cat, ok := DATA[baseSupCategryURl]; ok {
			cat.Products[pLink] = product
		}
		mu.Unlock()
	})

	detColl := tumbColl.Clone()

	catColl.OnError(errorHandler)
	tumbColl.OnError(errorHandler)
	detColl.OnError(errorHandler)

	catColl.Visit("https://garant1.ru/catalog")

	for l := range DATA {
		tumbColl.Visit(l)
	}
	tumbColl.Wait()

	for l, cat := range DATA {
		for i := 2; i <= cat.Pages; i++ {
			tumbColl.Visit(fmt.Sprintf("%s?page=%d", l, i))
		}
	}
	tumbColl.Wait()
}
