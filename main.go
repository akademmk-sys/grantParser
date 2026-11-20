package main

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gocolly/colly/v2"
	"github.com/xuri/excelize/v2"
)

type Product struct {
	PName       string
	PLink       string
	ImgLink     string
	Price       string
	Article     string
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
	if retryCount < 5 {
		retryCount++
		fmt.Printf("retry request (%d/5) on URL: %s\n", retryCount, r.Request.URL)
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

	tumbColl.OnHTML(`link[rel="next"]`, func(h *colly.HTMLElement) {
		next := h.Request.AbsoluteURL(h.Attr("href"))
		if next != "" {
			tumbColl.Visit(next)
		}
	})

	detColl := tumbColl.Clone()
	detColl.OnHTML("div.catalog", func(h *colly.HTMLElement) {
		var brand, price, article string
		h.ForEach("div.group", func(_ int, h *colly.HTMLElement) {
			key := strings.TrimSuffix(h.ChildText("div.group label.w_200"), ":")
			if key == "" {
				return
			}
			val := h.ChildText("label.w_200 + span")
			switch key {
			case "Артикул":
				article = val
			case "Производитель":
				brand = val
			case "Цена за упаковку":
				price = val
			}
		})

		rowImgLinks := h.ChildAttrs("a.big_img", "href")
		fullImgLinks := make([]string, 0, len(rowImgLinks))
		for _, el := range rowImgLinks {
			trimmed := strings.TrimSpace(el)
			fullImgLinks = append(fullImgLinks, h.Request.AbsoluteURL(trimmed))
		}
		productImgLinks := strings.Join(fullImgLinks, "\n")

		disc := h.ChildText(`[itemprop="description"]`)
		var specs []string
		h.ForEach(`[itemprop="description"] + .box ul > li`, func(_ int, h *colly.HTMLElement) {
			s := strings.Replace(h.Text, " : ", ": ", 1)
			if s != "" {
				specs = append(specs, s)
			}
		})
		discription := disc
		if len(specs) > 0 {
			discription += "\n\nХарактеристики:\n" + strings.Join(specs, "\n")
		}

		catKey := h.Request.Ctx.Get("cat")
		link := h.Request.Ctx.Get("link")

		mu.Lock()
		if cat, ok := DATA[catKey]; ok {
			if p, ok := cat.Products[link]; ok {
				p.ImgLink = productImgLinks
				p.Brand = brand
				p.Article = article
				p.Price = price
				p.Description = discription
				cat.Products[link] = p
			}
		}
		mu.Unlock()
	})

	catColl.OnError(errorHandler)
	tumbColl.OnError(errorHandler)
	detColl.OnError(errorHandler)

	catColl.Visit("https://garant1.ru/catalog")

	for l := range DATA {
		tumbColl.Visit(l)
	}
	tumbColl.Wait()

	for catKey, cat := range DATA {
		for link := range cat.Products {
			ctx := colly.NewContext()
			ctx.Put("cat", catKey)
			ctx.Put("link", link)
			if err := detColl.Request("GET", link, nil, ctx, nil); err != nil {
				fmt.Println("visit:", link, err)
			}
		}
	}
	detColl.Wait()

	exl := excelize.NewFile()
	defer func() {
		if err := exl.Close(); err != nil {
			fmt.Println(err)
		}
	}()

	exl.SetCellValue("Sheet1", "A1", "Категория")
	exl.SetCellValue("Sheet1", "B1", "Ссылка на категрию")
	exl.SetCellValue("Sheet1", "C1", "Товар")
	exl.SetCellValue("Sheet1", "D1", "Ссылка на товар")
	exl.SetCellValue("Sheet1", "E1", "Ссылка на картинку")
	exl.SetCellValue("Sheet1", "F1", "Цена")
	exl.SetCellValue("Sheet1", "G1", "Артикул")
	exl.SetCellValue("Sheet1", "H1", "Брэнд")
	exl.SetCellValue("Sheet1", "I1", "Описание")

	row := 2
	for _, cat := range DATA {
		for _, prod := range cat.Products {
			exl.SetCellValue("Sheet1", "A"+strconv.Itoa(row), cat.Name)
			exl.SetCellValue("Sheet1", "B"+strconv.Itoa(row), cat.Link)
			exl.SetCellValue("Sheet1", "C"+strconv.Itoa(row), prod.PName)
			exl.SetCellValue("Sheet1", "D"+strconv.Itoa(row), prod.PLink)
			exl.SetCellValue("Sheet1", "E"+strconv.Itoa(row), prod.ImgLink)
			exl.SetCellValue("Sheet1", "F"+strconv.Itoa(row), prod.Price)
			exl.SetCellValue("Sheet1", "G"+strconv.Itoa(row), prod.Article)
			exl.SetCellValue("Sheet1", "H"+strconv.Itoa(row), prod.Brand)
			exl.SetCellValue("Sheet1", "I"+strconv.Itoa(row), prod.Description)
			row++
		}
	}
	exl.SaveAs("grant_db.xlsx")
}
