package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image/color"
	"log"
	"math"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"go-hep.org/x/hep/hplot"

	//"github.com/dustin/go-humanize"
	_ "github.com/lib/pq"
	"github.com/mattn/go-nostrbuild"
	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
	"github.com/xhit/go-str2duration/v2"
	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/vg"
	"gonum.org/v1/plot/vg/draw"
)

const name = "nostr-coinchart"

const version = "0.0.3"

var revision = "HEAD"

type XTicks struct {
	Ticker plot.Ticker
	Time   func(t float64) time.Time
}

func (t XTicks) Ticks(min, max float64) []plot.Tick {
	ticks := []plot.Tick{}
	tmcur := time.Unix(int64(min), 0)
	tmmax := time.Unix(int64(max), 0)
	if max-min < 15000 {
		tmcur = time.Date(tmcur.Year(), tmcur.Month(), tmcur.Day(), tmcur.Hour(), tmcur.Minute()-tmcur.Minute()%10, 0, 0, tmcur.Location())
		tmmax = time.Date(tmmax.Year(), tmmax.Month(), tmmax.Day(), tmmax.Hour(), tmmax.Minute()-tmmax.Minute()%10, 0, 0, tmmax.Location())
	} else if max-min < 90000 {
		tmcur = time.Date(tmcur.Year(), tmcur.Month(), tmcur.Day(), tmcur.Hour(), 0, 0, 0, tmcur.Location())
		tmmax = time.Date(tmmax.Year(), tmmax.Month(), tmmax.Day(), tmmax.Hour(), 0, 0, 0, tmmax.Location())
	} else {
		tmcur = time.Date(tmcur.Year(), tmcur.Month(), tmcur.Day(), 0, 0, 0, 0, tmcur.Location())
		tmmax = time.Date(tmmax.Year(), tmmax.Month(), tmmax.Day(), 0, 0, 0, 0, tmmax.Location())
	}
	c := 0
	for {
		tick := plot.Tick{Value: float64(tmcur.Unix())}
		switch delta := max - min; {
		case delta < 864000:
			// delta is less than 10 days
			// - mayor: every day (min: 0, max: 10)
			// - minor: every day (min: 0, max: 10)
			if delta < 90000 {
				tick.Label = tmcur.Format("15:04")
			} else {
				tick.Label = tmcur.Format("01/02")
			}
			ticks = append(ticks, tick)
		case delta < 7776000:
			// delta is between 10 and 90 days
			// - mayor: every 5 days (min: 2, max: 18)
			// - minor: every day (min: 10, max: 90)
			if c%5 == 0 {
				tick.Label = tmcur.Format("01/02")
			}
			ticks = append(ticks, tick)
		case delta < 15552000:
			// delta is between 90 and 180 days
			// mayor: on day 1 and 15 of every month (min: 5, max: 12)
			// minor: on day 1, 5, 10, 15, 20, 25, 30 of every month (min: 17, max: 36)
			if tmcur.Day() == 1 || tmcur.Day() == 15 {
				tick.Label = tmcur.Format("01/02")
			}
			if tmcur.Day() == 1 || tmcur.Day()%5 == 0 {
				ticks = append(ticks, tick)
			}
		case delta < 47347200:
			// delta is between 6 months and 18 months
			// mayor: on day 1 of every month (min: 5, max: 18)
			// minor: on day 1 and 15 of every month (min: 11, max: 36)
			if tmcur.Day() == 1 {
				tick.Label = tmcur.Format("2004/01")
			}
			if tmcur.Day() == 1 || tmcur.Day() == 15 {
				ticks = append(ticks, tick)
			}
		default:
			// delta is higher than 18 months
			// mayor: on the 1st of january (min: 1, max: inf.)
			// minor: on day 1 of every month (min: 17, max inf.)
			if tmcur.Day() == 1 && tmcur.Month() == time.January {
				tick.Label = tmcur.Format("2004/01")
			}
			if tmcur.Day() == 1 {
				ticks = append(ticks, tick)
			}
		}
		c = c + 1
		if max-min < 15000 {
			tmcur = tmcur.Add(10 * time.Minute)
		} else if max-min < 87000 {
			tmcur = tmcur.Add(1 * time.Hour)
		} else {
			tmcur = tmcur.AddDate(0, 0, 1)
		}
		if tmcur.After(tmmax) {
			break
		}
	}
	return ticks
}

func generate(data [][]any, coin string, span int, sign func(*nostr.Event) error) (string, error) {
	if len(data) < 2 || len(data) > 43200 {
		return "", errors.New("invalid request")
	}

	sort.Slice(data, func(i, j int) bool {
		return data[i][0].(float64) < data[j][0].(float64)
	})

	var points plotter.XYs
	for _, d := range data {
		//println((data[len(data)-1][0].(float64) - d[0].(float64)) / 60000)
		if (data[len(data)-1][0].(float64)-d[0].(float64))/60000 > float64(span) {
			continue
		}
		v, err := strconv.ParseFloat(d[1].(string), 64)
		if err != nil {
			return "", errors.New("invalid data")
		}
		points = append(points, plotter.XY{
			X: d[0].(float64) / 1000,
			Y: v,
		})
	}

	dig := ""
	if strings.HasSuffix(coin, "JPY") {
		dig = "¥"
	} else if strings.HasSuffix(coin, "USD") {
		dig = "$"
	} else if strings.HasSuffix(coin, "BTC") {
		dig = "₿ "
	}
	if float64(math.Log10(points[len(points)-1].Y)) <= 2 {
		dig += "%0.4f"
	} else {
		dig += "%4.0f"
	}

	p := plot.New()
	p.Title.TextStyle.Color = color.White
	p.BackgroundColor = color.Black
	p.Title.Text = coin + " " + fmt.Sprintf(dig, points[len(points)-1].Y)
	p.Add(plotter.NewGrid())

	//p.X.Label.Text = "Time"
	p.X.Color = color.White
	p.X.Label.TextStyle.Color = color.White
	p.X.Label.Padding = vg.Points(10)
	p.X.LineStyle.Color = color.White
	p.X.LineStyle.Width = vg.Points(1)
	p.X.Tick.Color = color.White
	p.X.Tick.Marker = XTicks{}
	p.X.Tick.Label.Rotation = math.Pi / 3
	p.X.Tick.Label.XAlign = -1.2
	p.X.Tick.Label.Color = color.White

	//p.Y.Label.Text = "JPY/BTC"
	p.Y.Color = color.White
	p.Y.Label.TextStyle.Color = color.White
	p.Y.LineStyle.Color = color.White
	p.Y.LineStyle.Width = vg.Points(1)
	p.Y.Tick.Color = color.White
	p.Y.Tick.Label.Color = color.White
	p.Y.Tick.Marker = hplot.Ticks{
		N:      10,
		Format: dig,
	}
	p.Y.Tick.Label.Color = color.White
	p.Y.Label.Position = draw.PosRight
	p.X.Label.Position = draw.PosTop

	line, err := plotter.NewLine(points)
	if err != nil {
		log.Println(err)
	}
	line.Color = color.RGBA{R: 50, G: 255, B: 100, A: 255}
	p.Add(line)

	var buf bytes.Buffer
	w, err := p.WriterTo(5*vg.Inch, 4*vg.Inch, "png")
	if err != nil {
		return "", err
	}
	_, err = w.WriteTo(&buf)
	if err != nil {
		return "", err
	}

	result, err := nostrbuild.Upload(&buf, sign)
	if err != nil {
		return "", err
	}
	return result.Data[0].URL, nil
}

func handler(nsec string) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			return
		}
		var ev nostr.Event
		err := json.NewDecoder(r.Body).Decode(&ev)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		eev := nostr.Event{}
		var sk string
		if _, s, err := nip19.Decode(nsec); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		} else {
			sk = s.(string)
		}
		if pub, err := nostr.GetPublicKey(sk); err == nil {
			if _, err := nip19.EncodePublicKey(pub); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
			eev.PubKey = pub
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		sign := func(ev *nostr.Event) error {
			ev.PubKey = eev.PubKey
			return ev.Sign(sk)
		}

		pat := regexp.MustCompile(`^[A-Z]+$`)
		coin := "BTCJPY"
		tok := strings.Split(ev.Content, " ")
		span := 180
		interval := "1m"
		for i := 1; i < len(tok); i++ {
			tmp, err := str2duration.ParseDuration(tok[i])
			if err == nil {
				span = int(tmp.Minutes())
			} else if pat.MatchString(tok[i]) {
				coin = tok[i]
			} else {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}

		if span >= 3000 {
			interval = "30m"
		} else if span >= 1000 {
			interval = "5m"
		}
		resp, err := http.Get(fmt.Sprintf("https://api.binance.com/api/v3/klines?symbol=%s&interval=%s&limit=%d", coin, interval, span))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()

		var data [][]any
		err = json.NewDecoder(resp.Body).Decode(&data)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if span >= 3000 {
			interval = "30m"
		} else if span >= 1000 {
			interval = "5m"
		}

		img, err := generate(data, coin, span, sign)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		log.Println(img)
		eev.Content = img + "\n#ビットコインチャート"
		if coin != "BTCJPY" {
			eev.Content += " " + coin
		}
		eev.CreatedAt = nostr.Now()
		eev.Kind = ev.Kind
		eev.Tags = eev.Tags.AppendUnique(nostr.Tag{"e", ev.ID, "", "root"})
		eev.Tags = eev.Tags.AppendUnique(nostr.Tag{"p", ev.PubKey})
		eev.Tags = eev.Tags.AppendUnique(nostr.Tag{"t", "ビットコインチャート"})
		for _, te := range ev.Tags {
			if te.Key() == "e" {
				eev.Tags = eev.Tags.AppendUnique(te)
			}
		}
		eev.Sign(sk)

		w.Header().Set("content-type", "text/json; charset=utf-8")
		json.NewEncoder(w).Encode(eev)
	}
}

func init() {
}

func main() {
	var dsn string
	var ver bool
	var span time.Duration

	flag.StringVar(&dsn, "dsn", os.Getenv("DATABASE_URL"), "Database source")
	flag.DurationVar(&span, "span", 180*time.Minute, "span")
	flag.BoolVar(&ver, "v", false, "show version")
	flag.Parse()

	if ver {
		fmt.Println(version)
		os.Exit(0)
	}

	time.Local = time.FixedZone("Local", 9*60*60)

	nsec := os.Getenv("NULLPOGA_NSEC")
	if nsec == "" {
		log.Fatal("NULLPOGA_NSEC is not set")
	}

	http.HandleFunc("/", handler(nsec))
	addr := ":" + os.Getenv("PORT")
	if addr == ":" {
		addr = ":8080"
	}
	log.Printf("started %v", addr)
	http.ListenAndServe(addr, nil)
}
