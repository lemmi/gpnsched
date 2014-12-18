package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"text/template"
	"time"
)

var (
	CRLF       = []byte{'\r', '\n'}
	CRLFSP     = []byte{'\r', '\n', ' '}
	loc, _     = time.LoadLocation("Europe/Berlin")
	gpnstart   = time.Date(2013, 05, 30, 17, 23, 0, 0, loc)
	gpnstop    = time.Date(2013, 06, 02, 15, 30, 0, 0, loc)
	icals      = map[location][]byte{}
	icalsmutex = sync.RWMutex{}
)

func parsegpntime(t string, fallback time.Time) time.Time {
	var year, month, day, hour, min int
	n, err := fmt.Sscanf(t, "%04d%02d%02d-%02d%02d", &year, &month, &day, &hour, &min)
	if err != nil || n != 5 {
		return fallback
	}
	return time.Date(year, time.Month(month), day, hour, min, 0, 0, loc)
}

type BreakLongLineWriter struct {
	w      io.Writer
	buf    []byte
	maxlen int
	pos    int
}

func NewBreakLongLineWriter(w io.Writer, linelength int) io.Writer {
	return &BreakLongLineWriter{w: w, buf: []byte{}, maxlen: linelength, pos: 0}
}

func (b *BreakLongLineWriter) Write(p []byte) (int, error) {
	if len(b.buf) == 0 {
		b.buf = p
	} else {
		b.buf = append(b.buf, p...)
	}
	for len(b.buf) > 0 {
		adv, line, _ := bufio.ScanLines(b.buf, true)
		var n int
		for len(line) > 0 {
			adv, tok, _ := bufio.ScanRunes(line, false)
			if tok == nil {
				break
			}

			if b.pos+adv >= b.maxlen {
				b.w.Write(CRLFSP)
				b.pos = 1
			}

			c, err := b.w.Write(tok)
			if err != nil {
				return len(p), err
			}
			b.pos += c
			n += c
			line = line[c:]
		}
		if n == 0 {
			b.buf = append([]byte{}, b.buf...)
			break
		}
		b.buf = b.buf[adv:]
		if _, err := b.w.Write(CRLF); err != nil {
			return n, err
		}
		b.pos = 0
	}
	return len(p), nil
}

type location string

func (l location) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	fmt.Println(l)
	icalsmutex.RLock()
	w.Header().Add("Content-Type", "text/calendar")
	w.Header().Add("Content-Length", fmt.Sprintf("%d", len(icals[l])))
	w.Write(icals[l])
	icalsmutex.RUnlock()
}
func (l location) String() string {
	return string(l)
}

type event struct {
	Confirmed   string
	Start       string
	End         string
	Type        string
	Title       string
	Speaker     string
	Affiliation string
	Desc        string
	Long_desc   string
	Link        string
	Place       location
}

func (e *event) Starttime() time.Time {
	return parsegpntime(e.Start, gpnstart)
}

func (e *event) Endtime() time.Time {
	return parsegpntime(e.End, e.Starttime())
}

func (e *event) Titlestring() (ret string) {
	ret = "\"" + e.Title + "\""
	if e.Speaker != "" {
		ret += " - " + e.Speaker
	}
	if e.Affiliation != "" && e.Affiliation != e.Speaker {
		ret += " (" + e.Affiliation + ")"
	}
	return
}

func (e *event) Description() (ret string) {
	if e.Long_desc != "" {
		ret = e.Long_desc
	} else if e.Desc != "" {
		ret = e.Desc
	} else {
		ret = "No Description"
	}
	if e.Link != "" {
		ret = "\n\n" + e.Link
	}
	return
}

func (e *event) UID() (ret string) {
	hash := sha256.New()
	io.WriteString(hash, e.Start)
	io.WriteString(hash, e.Title)
	io.WriteString(hash, e.Place.String())

	return hex.EncodeToString(hash.Sum([]byte{}))
}

func icaldatetime(t time.Time) string {
	year, month, day := t.UTC().Date()
	hour, min, sec := t.UTC().Clock()
	return fmt.Sprintf("%04d%02d%02dT%02d%02d%02dZ", year, month, day, hour, min, sec)
}

var icalescape = strings.NewReplacer(
	"\\", "\\\\",
	"\n", "\\n",
	";", "\\;",
	",", "\\,",
).Replace

func icalformatline(w io.Writer, key, value string) {
	fmt.Fprintf(w, "%s:%s\r\n", key, icalescape(value))
}

func (e *event) VEVENT(w io.Writer) {
	icalformatline(w, "BEGIN", "VEVENT")
	icalformatline(w, "DTSTAMP", icaldatetime(time.Now()))
	icalformatline(w, "DTSTART", icaldatetime(e.Starttime()))
	icalformatline(w, "DTEND", icaldatetime(e.Endtime()))
	icalformatline(w, "SUMMARY", e.Titlestring())
	icalformatline(w, "DESCRIPTION", e.Description())
	icalformatline(w, "LOCATION", e.Place.String())
	icalformatline(w, "UID", e.UID())
	icalformatline(w, "END", "VEVENT")
}

type calendar []event

func (c calendar) ICal() []byte {
	var buf bytes.Buffer
	w := NewBreakLongLineWriter(&buf, 75)
	icalformatline(w, "BEGIN", "VCALENDAR")
	icalformatline(w, "VERSION", "2.0")
	icalformatline(w, "PRODID", "pff")

	for _, e := range c {
		e.VEVENT(w)
	}

	icalformatline(w, "END", "VCALENDAR")
	return buf.Bytes()
}

const htmltmpl = "" +
	`
<head>
<title>Fahrplaene</title>
</head>
<body>
{{range $room, $discard := . }}
<a href="/{{$room}}">{{$room}}</a><br/>
{{end}}
</body>
`

func synccalendars() {
	ticker := time.NewTicker(5 * time.Minute)
	for ; ; <-ticker.C {
		resp, err := http.Get("http://bl0rg.net/~andi/gpn13-fahrplan.json")
		if err != nil {
			panic(err)
		}
		defer resp.Body.Close()

		events := calendar{}
		dec := json.NewDecoder(resp.Body)
		err = dec.Decode(&events)
		if err != nil {
			panic(err)
		}

		builder := map[location]calendar{}
		for _, e := range events {
			builder[e.Place] = append(builder[e.Place], e)
		}

		icalsmutex.Lock()
		icals = map[location][]byte{}
		icals["Alle"] = events.ICal()
		for room, events := range builder {
			if room != "" {
				icals[room] = events.ICal()
			}
		}
		icalsmutex.Unlock()
	}
}

func handle(w http.ResponseWriter, r *http.Request) {
	if path := r.URL.Path; path == "/" {
		icalsmutex.RLock()
		tmpl := template.Must(template.New("html").Parse(htmltmpl))
		tmpl.Execute(w, icals)
		icalsmutex.RUnlock()
	} else {
		location(path[1:]).ServeHTTP(w, r)
	}
}

func main() {
	go synccalendars()
	http.HandleFunc("/", handle)
	if err := http.ListenAndServe(":8000", nil); err != nil {
		panic(err)
	}
}
