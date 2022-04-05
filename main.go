package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"database/sql"
	"fmt"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/chromedp"
	"github.com/essentialkaos/translit"
	"github.com/go-telegram-bot-api/telegram-bot-api"
	_ "github.com/mattn/go-sqlite3"
	"io/ioutil"
	"log"
	"net/http"
	"regexp"
	"time"
)

func main() {
	go ajsaleParser()
	go ajParser()

	bot, err := tgbotapi.NewBotAPI("489336826:AAFff3rB17QW4_ICh3HtOd6hkoVa-zfzxMo")
	if err != nil {
		log.Panic(err)
	}

	bot.Debug = true

	log.Printf("Authorized on account %s", bot.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates, err := bot.GetUpdatesChan(u)

	database, _ := sql.Open("sqlite3", "./applebot.db")
	statement, _ := database.Prepare("CREATE TABLE IF NOT EXISTS prices (id INTEGER PRIMARY KEY, title TEXT, category TEXT, command TEXT, price TEXT, description TEXT, link TEXT, site TEXT, updated TEXT, other_signs TEXT)")
	statement.Exec()

	for update := range updates {
		if update.Message == nil { // ignore any non-Message Updates
			continue
		}

		log.Printf("[%s] %s", update.Message.From.UserName, update.Message.Text)

		msg := tgbotapi.NewMessage(update.Message.Chat.ID, update.Message.Text)
		msg.ReplyToMessageID = update.Message.MessageID

		if update.Message.IsCommand() {
			msg := tgbotapi.NewMessage(update.Message.Chat.ID, "")

			rows, err := database.Query(`SELECT command FROM prices WHERE site="aj.ru" GROUP BY command`)
			if err != nil {
				log.Fatal(err)
			}
			defer rows.Close()

			var command = ""
			for rows.Next() {
				err := rows.Scan(&command)
				if err != nil {
					log.Fatal(err)
				}
				if update.Message.Command() == command {
					rows, err := database.Query(`SELECT title, price, description FROM prices WHERE site="aj.ru" AND command=? ORDER BY title COLLATE NOCASE ASC`, command)
					if err != nil {
						log.Fatal(err)
					}
					defer rows.Close()
					var items = ""
					var title = ""
					var price = ""
					var description sql.NullString
					for rows.Next() {
						err := rows.Scan(&title, &price, &description)
						if err != nil {
							log.Fatal(err)
						}

						items += title + " – *" + price + "* руб.\n"
					}
					msg.Text = items
					msg.ParseMode = "markdown"
					sendedMsg, err := bot.Send(msg)
					time.AfterFunc(60*time.Second, func() { bot.DeleteMessage(tgbotapi.DeleteMessageConfig{sendedMsg.Chat.ID, sendedMsg.MessageID}) })
					time.AfterFunc(60*time.Second, func() { bot.DeleteMessage(tgbotapi.DeleteMessageConfig{sendedMsg.Chat.ID, update.Message.MessageID}) })

				}
			}

			switch update.Message.Command() {
			case "aj":
				rows, err := database.Query(`SELECT command FROM prices WHERE site="aj.ru" GROUP BY command ORDER BY command COLLATE NOCASE ASC`)
				if err != nil {
					log.Fatal(err)
				}
				defer rows.Close()

				var categories, command string
				for rows.Next() {
					err := rows.Scan(&command)
					if err != nil {
						log.Fatal(err)
					}

					categories += "/" + command + "\n"
				}
				msg.Text = categories
				sentMsg, err := bot.Send(msg)
				time.AfterFunc(60*time.Second, func() { bot.DeleteMessage(tgbotapi.DeleteMessageConfig{sentMsg.Chat.ID, sentMsg.MessageID}) })
				time.AfterFunc(60*time.Second, func() { bot.DeleteMessage(tgbotapi.DeleteMessageConfig{sentMsg.Chat.ID, update.Message.MessageID}) })
			case "ajsale":
				rows, err := database.Query(`SELECT title, price, description FROM prices WHERE site="ajsale.ru" ORDER BY category COLLATE NOCASE ASC`)
				if err != nil {
					log.Fatal(err)
				}
				defer rows.Close()

				var items, title, price, description string
				for rows.Next() {
					err := rows.Scan(&title, &price, &description)
					if err != nil {
						log.Fatal(err)
					}

					//log.Println(p.id, p.title)
					items += title + " – *" + price + "* руб.\n"
				}
				msg.Text = items
				msg.ParseMode = "markdown"
				sendedMsg, err := bot.Send(msg)
				time.AfterFunc(60*time.Second, func() { bot.DeleteMessage(tgbotapi.DeleteMessageConfig{sendedMsg.Chat.ID, sendedMsg.MessageID}) })
				time.AfterFunc(60*time.Second, func() { bot.DeleteMessage(tgbotapi.DeleteMessageConfig{sendedMsg.Chat.ID, update.Message.MessageID}) })
			case "changes":
				rows, err := database.Query(`SELECT title, price, old_price, updated FROM updates WHERE site="aj.ru" AND DATE(updated) >= DATE('now', 'weekday 0', '-7 days') ORDER BY id DESC LIMIT 30`)
				if err != nil {
					log.Fatal(err)
				}
				defer rows.Close()

				var items, title, price, old_price, updated string
				for rows.Next() {
					rows.Scan(&title, &price, &old_price, &updated)
					updated, _ := time.Parse(time.RFC3339, updated)
					location, _ := time.LoadLocation("Europe/Moscow")
					items += title + " было *" + old_price + "* руб. стало *" + price + "* руб. (" + updated.In(location).Format("02.01.2006 15:04:05") + ")\n"
				}
				msg.Text = items
				msg.ParseMode = "markdown"
				sentMsg, _ := bot.Send(msg)
				time.AfterFunc(60*time.Second, func() { bot.DeleteMessage(tgbotapi.DeleteMessageConfig{sentMsg.Chat.ID, sentMsg.MessageID}) })
				time.AfterFunc(60*time.Second, func() { bot.DeleteMessage(tgbotapi.DeleteMessageConfig{sentMsg.Chat.ID, update.Message.MessageID}) })
			}

		}
	}

}

func ajParser() {
	database, _ := sql.Open("sqlite3", "./applebot.db")
	statement, _ := database.Prepare("CREATE TABLE IF NOT EXISTS prices (id INTEGER PRIMARY KEY, title TEXT, category TEXT, command TEXT, price TEXT, description TEXT, link TEXT, site TEXT, updated TEXT, other_signs TEXT)")
	statement.Exec()
	statement, _ = database.Prepare("CREATE TABLE IF NOT EXISTS updates (id INTEGER PRIMARY KEY, title TEXT, category TEXT, price TEXT, description TEXT, link TEXT, site TEXT, updated TEXT, other_signs TEXT, update_type TEXT, old_price TEXT)")
	statement.Exec()

	http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}

	ticker := time.NewTicker(5 * 60 * time.Second)
	for ; true; <-ticker.C {

		datetime := time.Now().Format(time.RFC3339)
		fmt.Println("ajParser " + datetime)

		resp, err := http.Get("https://aj.ru")
		if err != nil {
			panic(err)
		}
		defer resp.Body.Close()
		html, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			panic(err)
		}

		type Price struct {
			id          int
			title       []byte
			category    []byte
			command     []byte
			price       []byte
			description []byte
			link        []byte
			site        []byte
			updated     []byte
			otherSigns  []byte
		}

		rows, err := database.Query(`SELECT * FROM prices WHERE site="aj.ru";`)
		if err != nil {
			log.Fatal(err)
		}
		defer rows.Close()

		var prices []Price
		for rows.Next() {
			var p Price
			err := rows.Scan(&p.id, &p.title, &p.category, &p.command, &p.price, &p.description, &p.link, &p.site, &p.updated, &p.otherSigns)
			if err != nil {
				log.Fatal(err)
			}

			//log.Println(p.id, p.title)
			prices = append(prices, Price{id: p.id, title: p.title, category: p.category, command: p.command, price: p.price, description: p.description, link: p.link, site: p.site, updated: p.updated, otherSigns: p.otherSigns})
		}
		if err := rows.Err(); err != nil {
			log.Fatal(err)
		}

		//log.Println(prices)

		statement, _ = database.Prepare("DELETE FROM prices WHERE site='aj.ru'")
		statement.Exec()

		reComments := regexp.MustCompile(`(?is)<!--(.*?)-->`)
		html = reComments.ReplaceAll(html, []byte(""))

		reArticle := regexp.MustCompile("(?is)<article(.*?)</article>")
		articles := reArticle.FindAll(html, -1)
		for _, article := range articles {

			reClass := regexp.MustCompile("(?is)<article(.*?)class=\"(.*?)\"")
			classes := reClass.FindAllSubmatch(article, -1)
			classesString := []byte("")
			if len(classes) > 0 {
				classesString = classes[0][2]
			}

			category := []byte("")

			reH2 := regexp.MustCompile("(?is)<h2(.*?)>(.*?)</h2>")
			h2 := reH2.FindAllSubmatch(article, -1)
			category = h2[0][2]

			reH3 := regexp.MustCompile("(?is)<h3(.*?)>(.*?)</h3>")
			h3 := reH3.FindAllSubmatch(article, -1)
			description := []byte("")
			if len(h3) > 0 {
				description = h3[0][2]
			}

			reProduct := regexp.MustCompile(`(?im)<li>(.*?)<span>([ \d]+)</span>(.*)₽`)
			products := reProduct.FindAllSubmatch(article, -1)
			if products == nil {
				reProduct := regexp.MustCompile(`(?im)<td>(.*?)<span>([ \d]+)</span>(.*)₽`)
				products = reProduct.FindAllSubmatch(article, -1)
			}

			if len(products) > 0 {
				for _, product := range products {
					statement, _ = database.Prepare("INSERT INTO prices (title, category, command, description, price, site, updated, other_signs) VALUES (?, ?,?, ?, ?, ?, ?,?)")
					datetime := time.Now().Format(time.RFC3339)

					price := bytes.ReplaceAll(product[2], []byte(" "), []byte(""))
					title := []byte("")
					if len(product[1]) > 0 {
						title = product[1]
					} else {
						title = category
					}
					reTags := regexp.MustCompile(`<(.|\n)*?>|&mdash;`)
					title = reTags.ReplaceAll(title, []byte(""))
					title = bytes.TrimSpace(title)
					category = reTags.ReplaceAll(category, []byte(""))
					category = bytes.TrimSpace(category)
					regLetters, _ := regexp.Compile("[^a-zA-Z0-9а-яА-Я]+")
					command := translit.EncodeToICAO(string(regLetters.ReplaceAll(category, []byte(""))))
					description = reTags.ReplaceAll(description, []byte(""))
					description = bytes.TrimSpace(description)

					for _, priceItem := range prices {
						if bytes.Compare(priceItem.title, title) == 0 &&
							bytes.Compare(priceItem.category, category) == 0 &&
							bytes.Compare(priceItem.otherSigns, classesString) == 0 &&
							bytes.Compare(priceItem.description, description) == 0 {
							if bytes.Compare(priceItem.price, price) != 0 {
								statement2, _ := database.Prepare("INSERT INTO updates (title, category, description, price, site, updated,other_signs, update_type,old_price) VALUES (?, ?, ?, ?, ?, ?,?,?,?)")
								updateType := "ChangedPrice"
								oldPrice := priceItem.price
								statement2.Exec(title, category, description, price, "aj.ru", datetime, classesString, updateType, oldPrice)
							}
						}
					}

					statement.Exec(title, category, command, description, price, "aj.ru", datetime, classesString)
				}
			}
		}
	}
}

func ajsaleParser() {
	database, _ := sql.Open("sqlite3", "./applebot.db")
	statement, _ := database.Prepare("CREATE TABLE IF NOT EXISTS prices (id INTEGER PRIMARY KEY, title TEXT, category TEXT, price TEXT, description TEXT, link TEXT, site TEXT, updated TEXT, other_signs TEXT)")
	statement.Exec()
	statement, _ = database.Prepare("CREATE TABLE IF NOT EXISTS updates (id INTEGER PRIMARY KEY, title TEXT, category TEXT, price TEXT, description TEXT, link TEXT, site TEXT, updated TEXT, other_signs TEXT, update_type TEXT, old_price TEXT)")
	statement.Exec()

	ctx, cancel := chromedp.NewContext(context.Background())
	defer cancel()

	ticker := time.NewTicker(5 * 60 * time.Second)

	for ; true; <-ticker.C {

		datetime := time.Now().Format(time.RFC3339)
		fmt.Println("ajsaleParser " + datetime)

		var res string

		err := chromedp.Run(ctx,
			chromedp.Navigate(`https://ajsale.ru`),
			chromedp.WaitReady(".catalog__item"),
			chromedp.ActionFunc(func(ctx context.Context) error {
				node, err := dom.GetDocument().Do(ctx)
				if err != nil {
					return err
				}
				res, err = dom.GetOuterHTML().WithNodeID(node.NodeID).Do(ctx)
				return err
			}),
		)

		if err != nil {
			fmt.Println(err)
		}

		statement, _ = database.Prepare("DELETE FROM prices WHERE site='ajsale.ru'")
		statement.Exec()

		reProduct := regexp.MustCompile(`(?im)<div class="catalog__item"(.*?)<h3>(.*?)<\/h3>(.*?)<p class="caption__desc">(.*?)<\/p>(.*?)<p class="caption__price"><span>(.*?)<\/span>(.*?)Оформить заявку`)
		products := reProduct.FindAllSubmatch([]byte(res), -1)

		for _, product := range products {
			statement, _ = database.Prepare("INSERT INTO prices (title, price, category, description, site, updated) VALUES (?, ?, ?, ?, ?, ?)")
			datetime := time.Now().Format(time.RFC3339)
			price := bytes.ReplaceAll(product[6], []byte("&nbsp;"), []byte(""))
			price = bytes.ReplaceAll(price, []byte("₽"), []byte(""))
			price = bytes.TrimSpace(price)
			title := bytes.TrimSpace(product[2])
			reTags := regexp.MustCompile(`<(.|\n)*?>|&mdash;`)
			description := reTags.ReplaceAll(product[4], []byte(""))
			description = bytes.TrimSpace(description)
			statement.Exec(title, price, "sale", description, "ajsale.ru", datetime)
		}
	}
}
