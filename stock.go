package main

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/go-redis/redis/v8"

	"github.com/rssnyder/discord-stock-ticker/utils"
)

type Stock struct {
	Ticker    string          `json:"ticker"`   // stock symbol
	Name      string          `json:"name"`     // override for symbol as shown on the bot
	Nickname  bool            `json:"nickname"` // flag for changing nickname
	Color     bool            `json:"color"`
	Decorator string          `json:"decorator"`
	Frequency time.Duration   `json:"frequency"` // how often to update in seconds
	Currency  string          `json:"currency"`
	Bitcoin   bool            `json:"bitcoin"`
	Activity  string          `json:"activity"`
	Cache     *redis.Client   `json:"-"`
	Context   context.Context `json:"-"`
	token     string          `json:"-"` // discord token
	close     chan int        `json:"-"`
}

// NewStock saves information about the stock and starts up a watcher on it
func NewStock(ticker string, token string, name string, nickname bool, color bool, decorator string, frequency int, currency string, activity string) *Stock {
	s := &Stock{
		Ticker:    ticker,
		Name:      name,
		Nickname:  nickname,
		Color:     color,
		Decorator: decorator,
		Activity:  activity,
		Frequency: time.Duration(frequency) * time.Second,
		Currency:  strings.ToUpper(currency),
		token:     token,
		close:     make(chan int, 1),
	}

	// spin off go routine to watch the price
	go s.watchStockPrice()
	return s
}

// NewCrypto saves information about the crypto and starts up a watcher on it
func NewCrypto(ticker string, token string, name string, nickname bool, color bool, decorator string, frequency int, currency string, bitcoin bool, activity string, cache *redis.Client, context context.Context) *Stock {
	s := &Stock{
		Ticker:    ticker,
		Name:      name,
		Nickname:  nickname,
		Color:     color,
		Decorator: decorator,
		Activity:  activity,
		Frequency: time.Duration(frequency) * time.Second,
		Currency:  strings.ToUpper(currency),
		Bitcoin:   bitcoin,
		Cache:     cache,
		Context:   context,
		token:     token,
		close:     make(chan int, 1),
	}

	// spin off go routine to watch the price
	go s.watchCryptoPrice()
	return s
}

// Shutdown sends a signal to shut off the goroutine
func (s *Stock) Shutdown() {
	s.close <- 1
}

func (s *Stock) watchStockPrice() {
	var exRate float64

	// create a new discord session using the provided bot token.
	dg, err := discordgo.New("Bot " + s.token)
	if err != nil {
		fmt.Println("Error creating Discord session: ", err)
		return
	}

	// show as online
	err = dg.Open()
	if err != nil {
		fmt.Println("error opening discord connection,", err)
		return
	}

	// get bot id
	botUser, err := dg.User("@me")
	if err != nil {
		fmt.Println("Error getting bot id: ", err)
		return
	}

	// Get guides for bot
	guilds, err := dg.UserGuilds(100, "", "")
	if err != nil {
		fmt.Println("Error getting guilds: ", err)
		s.Nickname = false
	}

	// If other currency, get rate
	if s.Currency != "USD" {
		exData, err := utils.GetStockPrice(s.Currency + "=X")
		if err != nil {
			logger.Errorf("Unable to fetch exchange rate for %s, default to USD.", s.Currency)
		} else {
			exRate = exData.QuoteSummary.Results[0].Price.RegularMarketPrice.Raw
		}
	}

	// Set arrows if no custom decorator
	var arrows bool
	if s.Decorator == "" {
		arrows = true
	}

	// Grab custom activity messages
	var custom_activity []string
	itr := 0
	itrSeed := 0.0
	if s.Activity != "" {
		custom_activity = strings.Split(s.Activity, ";")
	}

	logger.Infof("Watching stock price for %s", s.Name)
	ticker := time.NewTicker(s.Frequency)

	// continuously watch
	for {
		select {
		case <-s.close:
			logger.Infof("Shutting down price watching for %s", s.Name)
			return
		case <-ticker.C:
			logger.Infof("Fetching stock price for %s", s.Name)

			var priceData utils.PriceResults
			var fmtPrice string
			var fmtDiffPercent string
			var fmtDiffChange string

			// save the price struct & do something with it
			priceData, err = utils.GetStockPrice(s.Ticker)
			if err != nil {
				logger.Errorf("Unable to fetch stock price for %s", s.Name)
			}

			if len(priceData.QuoteSummary.Results) == 0 {
				logger.Errorf("Yahoo returned bad data for %s", s.Name)
				continue
			}
			fmtPrice = priceData.QuoteSummary.Results[0].Price.RegularMarketPrice.Fmt

			// Check if conversion is needed
			if exRate != 0 {
				rawPrice := exRate * priceData.QuoteSummary.Results[0].Price.RegularMarketPrice.Raw
				fmtPrice = strconv.FormatFloat(rawPrice, 'f', 2, 64)
			}

			// check for day or after hours change
			if priceData.QuoteSummary.Results[0].Price.MarketState == "POST" {
				fmtDiffPercent = priceData.QuoteSummary.Results[0].Price.PostMarketChangePercent.Fmt
				fmtDiffChange = priceData.QuoteSummary.Results[0].Price.PostMarketChange.Fmt
			} else if priceData.QuoteSummary.Results[0].Price.MarketState == "PRE" {
				fmtDiffPercent = priceData.QuoteSummary.Results[0].Price.PreMarketChangePercent.Fmt
				fmtDiffChange = priceData.QuoteSummary.Results[0].Price.PreMarketChange.Fmt
			} else {
				fmtDiffPercent = priceData.QuoteSummary.Results[0].Price.RegularMarketChangePercent.Fmt
				fmtDiffChange = priceData.QuoteSummary.Results[0].Price.RegularMarketChange.Fmt
			}

			// calculate if price has moved up or down
			var increase bool
			if len(fmtDiffChange) == 0 {
				increase = true
			} else if string(fmtDiffChange[0]) == "-" {
				increase = false
			} else {
				increase = true
			}

			if arrows {
				s.Decorator = "⬊"
				if increase {
					s.Decorator = "⬈"
				}
			}

			if s.Nickname {
				// update nickname instead of activity
				var nickname string
				var activity string

				// format nickname & activity
				nickname = fmt.Sprintf("%s %s $%s", strings.ToUpper(s.Name), s.Decorator, fmtPrice)
				activity = fmt.Sprintf("$%s (%s)", fmtDiffChange, fmtDiffPercent)

				// Update nickname in guilds
				for _, g := range guilds {
					err = dg.GuildMemberNickname(g.ID, "@me", nickname)
					if err != nil {
						fmt.Println("Error updating nickname: ", err)
						continue
					}
					logger.Infof("Set nickname in %s: %s", g.Name, nickname)

					if s.Color {
						// get roles for colors
						var redRole string
						var greeenRole string

						roles, err := dg.GuildRoles(g.ID)
						if err != nil {
							fmt.Println("Error getting guilds: ", err)
							continue
						}

						// find role ids
						for _, r := range roles {
							if r.Name == "tickers-red" {
								redRole = r.ID
							} else if r.Name == "tickers-green" {
								greeenRole = r.ID
							}
						}

						if len(redRole) == 0 || len(greeenRole) == 0 {
							logger.Error("Unable to find roles for color changes")
							continue
						}

						// assign role based on change
						if increase {
							err = dg.GuildMemberRoleRemove(g.ID, botUser.ID, redRole)
							if err != nil {
								logger.Error("Unable to remove role: ", err)
							}
							err = dg.GuildMemberRoleAdd(g.ID, botUser.ID, greeenRole)
							if err != nil {
								logger.Error("Unable to set role: ", err)
							}
						} else {
							err = dg.GuildMemberRoleRemove(g.ID, botUser.ID, greeenRole)
							if err != nil {
								logger.Error("Unable to remove role: ", err)
							}
							err = dg.GuildMemberRoleAdd(g.ID, botUser.ID, redRole)
							if err != nil {
								logger.Error("Unable to set role: ", err)
							}
						}
					}
				}

				// Custom activity messages
				if len(custom_activity) > 0 {

					// Display the real activity once per cycle
					if itr == len(custom_activity) {
						itr = 0
						itrSeed = 0.0
					} else if math.Mod(itrSeed, 2.0) == 1.0 {
						activity = custom_activity[itr]
						itr++
						itrSeed++
					} else {
						activity = custom_activity[itr]
						itrSeed++
					}
				}

				err = dg.UpdateGameStatus(0, activity)
				if err != nil {
					logger.Error("Unable to set activity: ", err)
				} else {
					logger.Infof("Set activity: %s", activity)
				}

			} else {
				activity := fmt.Sprintf("%s %s %s", fmtPrice, s.Decorator, fmtDiffPercent)

				err = dg.UpdateGameStatus(0, activity)
				if err != nil {
					logger.Error("Unable to set activity: ", err)
				} else {
					logger.Infof("Set activity: %s", activity)
				}

			}

		}

	}

}

func (s *Stock) watchCryptoPrice() {
	var rdb *redis.Client
	var exRate float64

	// create a new discord session using the provided bot token.
	dg, err := discordgo.New("Bot " + s.token)
	if err != nil {
		fmt.Println("Error creating Discord session: ", err)
		return
	}

	// show as online
	err = dg.Open()
	if err != nil {
		fmt.Println("error opening discord connection,", err)
		return
	}

	// get bot id
	botUser, err := dg.User("@me")
	if err != nil {
		fmt.Println("Error getting bot id: ", err)
		return
	}

	// Get guides for bot
	guilds, err := dg.UserGuilds(100, "", "")
	if err != nil {
		fmt.Println("Error getting guilds: ", err)
		s.Nickname = false
	}

	// If other currency, get rate
	if s.Currency != "USD" {
		exData, err := utils.GetStockPrice(s.Currency + "=X")
		if err != nil {
			logger.Errorf("Unable to fetch exchange rate for %s, default to USD.", s.Currency)
		} else {
			exRate = exData.QuoteSummary.Results[0].Price.RegularMarketPrice.Raw
		}
	}

	// Set arrows if no custom decorator
	var arrows bool
	if s.Decorator == "" {
		arrows = true
	}

	// Grab custom activity messages
	var custom_activity []string
	itr := 0
	itrSeed := 0.0
	if s.Activity != "" {
		custom_activity = strings.Split(s.Activity, ";")
	}

	ticker := time.NewTicker(s.Frequency)
	logger.Debugf("Watching crypto price for %s", s.Name)

	// continuously watch
	for {
		select {
		case <-s.close:
			logger.Infof("Shutting down price watching for %s", s.Name)
			return
		case <-ticker.C:
			logger.Debugf("Fetching crypto price for %s", s.Name)

			var priceData utils.GeckoPriceResults
			var fmtPrice string
			var fmtChange string
			var changeHeader string
			var fmtDiffPercent string

			// save the price struct & do something with it
			if s.Cache == rdb {
				priceData, err = utils.GetCryptoPrice(s.Name)
			} else {
				priceData, err = utils.GetCryptoPriceCache(s.Cache, s.Context, s.Name)
			}
			if err != nil {
				logger.Errorf("Unable to fetch stock price for %s: %s", s.Name, err)
			}

			// Check if conversion is needed
			if exRate != 0 {
				priceData.MarketData.CurrentPrice.USD = exRate * priceData.MarketData.CurrentPrice.USD
				priceData.MarketData.PriceChangeCurrency.USD = exRate * priceData.MarketData.PriceChangeCurrency.USD
			}

			fmtDiffPercent = fmt.Sprintf("%.2f", priceData.MarketData.PriceChangePercent)

			// Check if a crypto pair is set
			if s.Bitcoin {
				changeHeader = "₿"

				fmtPrice = fmt.Sprintf("₿%.6f", priceData.MarketData.CurrentPrice.BTC)
				fmtChange = fmt.Sprintf("%.2f", priceData.MarketData.PriceChangeCurrency.BTC)

			} else {
				changeHeader = "$"

				// Check for cryptos below 1c
				if priceData.MarketData.CurrentPrice.USD < 0.01 {
					priceData.MarketData.CurrentPrice.USD = priceData.MarketData.CurrentPrice.USD * 100
					if priceData.MarketData.CurrentPrice.USD < 0.00001 {
						fmtPrice = fmt.Sprintf("%.8f¢", priceData.MarketData.CurrentPrice.USD)
					} else {
						fmtPrice = fmt.Sprintf("%.6f¢", priceData.MarketData.CurrentPrice.USD)
					}
					fmtChange = fmt.Sprintf("%.2f", priceData.MarketData.PriceChangeCurrency.USD)
				} else if priceData.MarketData.CurrentPrice.USD < 1.0 {
					fmtPrice = fmt.Sprintf("$%.3f", priceData.MarketData.CurrentPrice.USD)
					fmtChange = fmt.Sprintf("%.2f", priceData.MarketData.PriceChangeCurrency.USD)
				} else {
					fmtPrice = fmt.Sprintf("$%.2f", priceData.MarketData.CurrentPrice.USD)
					fmtChange = fmt.Sprintf("%.2f", priceData.MarketData.PriceChangeCurrency.USD)
				}
			}

			// calculate if price has moved up or down
			var increase bool
			if len(fmtChange) == 0 {
				increase = true
			} else if string(fmtChange[0]) == "-" {
				increase = false
			} else {
				increase = true
			}

			if arrows {
				s.Decorator = "⬊"
				if increase {
					s.Decorator = "⬈"
				}
			}

			if s.Nickname {
				// update nickname instead of activity
				var displayName string
				var nickname string
				var activity string

				if s.Ticker != "" {
					displayName = s.Ticker
				} else {
					displayName = strings.ToUpper(priceData.Symbol)
				}

				// format nickname
				nickname = fmt.Sprintf("%s %s %s", displayName, s.Decorator, fmtPrice)
				activity = fmt.Sprintf("%s%s (%s%%)", changeHeader, fmtChange, fmtDiffPercent)

				// Update nickname in guilds
				for _, g := range guilds {
					err = dg.GuildMemberNickname(g.ID, "@me", nickname)
					if err != nil {
						fmt.Println("Error updating nickname: ", err)
						continue
					}
					logger.Infof("Set nickname in %s: %s", g.Name, nickname)

					if s.Color {
						// get roles for colors
						var redRole string
						var greeenRole string

						roles, err := dg.GuildRoles(g.ID)
						if err != nil {
							fmt.Println("Error getting guilds: ", err)
							continue
						}

						// find role ids
						for _, r := range roles {
							if r.Name == "tickers-red" {
								redRole = r.ID
							} else if r.Name == "tickers-green" {
								greeenRole = r.ID
							}
						}

						if len(redRole) == 0 || len(greeenRole) == 0 {
							logger.Error("Unable to find roles for color changes")
							continue
						}

						// assign role based on change
						if increase {
							err = dg.GuildMemberRoleRemove(g.ID, botUser.ID, redRole)
							if err != nil {
								logger.Error("Unable to remove role: ", err)
							}
							err = dg.GuildMemberRoleAdd(g.ID, botUser.ID, greeenRole)
							if err != nil {
								logger.Error("Unable to set role: ", err)
							}
						} else {
							err = dg.GuildMemberRoleRemove(g.ID, botUser.ID, greeenRole)
							if err != nil {
								logger.Error("Unable to remove role: ", err)
							}
							err = dg.GuildMemberRoleAdd(g.ID, botUser.ID, redRole)
							if err != nil {
								logger.Error("Unable to set role: ", err)
							}
						}
					}
				}

				// Custom activity messages
				if len(custom_activity) > 0 {

					// Display the real activity once per cycle
					if itr == len(custom_activity) {
						itr = 0
						itrSeed = 0.0
					} else if math.Mod(itrSeed, 2.0) == 1.0 {
						activity = custom_activity[itr]
						itr++
						itrSeed++
					} else {
						activity = custom_activity[itr]
						itrSeed++
					}
				}

				err = dg.UpdateGameStatus(0, activity)
				if err != nil {
					logger.Error("Unable to set activity: ", err)
				} else {
					logger.Infof("Set activity: %s", activity)
				}

			} else {

				// format activity
				activity := fmt.Sprintf("%s %s %s%%", fmtPrice, s.Decorator, fmtDiffPercent)
				err = dg.UpdateGameStatus(0, activity)
				if err != nil {
					logger.Error("Unable to set activity: ", err)
				} else {
					logger.Infof("Set activity: %s", activity)
				}

			}

		}
	}
}
