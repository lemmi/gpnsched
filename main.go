package main

import (
	"encoding/json"
	"net/http"
	"fmt"
	"time"
	"bytes"
	"text/template"
	"sync"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strings"
)

var (
	CRLF = []byte{'\r', '\n'}
	CRLFSP = []byte{'\r', '\n', ' '}
	loc, _ = time.LoadLocation("Europe/Berlin")
	gpnstart = time.Date(2013, 05, 30, 17, 23, 0, 0, loc)
	gpnstop = time.Date(2013, 06, 02, 15, 30, 0, 0, loc)
	icals = map[location][]byte{}
	icalsmutex = sync.RWMutex{}
)

func parsegpntime(t string, fallback time.Time) time.Time {
	var year, month, day, hour, min int
	n, err := fmt.Sscanf(t, "%04d%02d%02d-%02d%02d", &year, &month, &day, &hour, &min)
	if err != nil || n != 5 { return fallback }
	return time.Date(year, time.Month(month), day, hour, min, 0, 0, loc)
}

func breaklongline(line string) string {
	var buf bytes.Buffer
	segments := 1
	for pos, char := range line {
		if pos > 75 * segments {
			segments++
			buf.Write(CRLFSP)
		}
		buf.WriteRune(char)
	}
	return buf.String()
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
	var buf bytes.Buffer
	buf.WriteString(e.Start)
	buf.WriteString(e.Title)
	buf.WriteString(e.Place.String())

	hash := sha256.New()
	io.Copy(hash, &buf)
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
	";",  "\\;",
	",",  "\\,",
).Replace

func icalformatline(key, value string) string {
	return breaklongline(key + ":" + icalescape(value))
}

func (e *event) VEVENT() (ret []string) {
	lines := map[string]string{}
	lines["DTSTAMP"]     = icaldatetime(time.Now())
	lines["DTSTART"]     = icaldatetime(e.Starttime())
	lines["DTEND"]       = icaldatetime(e.Endtime())
	lines["SUMMARY"]     = e.Titlestring()
	lines["DESCRIPTION"] = e.Description()
	lines["LOCATION"]    = string(e.Place)
	lines["UID"]         = e.UID()

	ret = append(ret, icalformatline("BEGIN", "VEVENT"))
	for key, value := range lines {
		ret = append(ret, icalformatline(key, value))
	}
	ret = append(ret, icalformatline("END", "VEVENT"))
	return
}

type calendar []event
func (c calendar) ICal() []byte {
	var buf bytes.Buffer
	buf.WriteString(icalformatline("BEGIN", "VCALENDAR"))
	buf.Write(CRLF)
	buf.WriteString(icalformatline("VERSION", "2.0"))
	buf.Write(CRLF)
	buf.WriteString(icalformatline("PRODID", "pff"))
	buf.Write(CRLF)

	for _, e := range c {
		for _, line := range (&e).VEVENT() {
			buf.WriteString(line)
			buf.Write(CRLF)
		}
	}

	buf.WriteString("END:VCALENDAR")
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
	ticker := time.NewTicker(5*time.Minute)
	for ; ; <-ticker.C {
		resp, err := http.Get("http://bl0rg.net/~andi/gpn13-fahrplan.json")
		if err != nil { panic(err) }
		defer resp.Body.Close()

		events := calendar{}
		dec := json.NewDecoder(resp.Body)
		err = dec.Decode(&events)
		if err != nil { panic(err) }

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
