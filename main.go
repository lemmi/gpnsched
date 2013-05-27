package main

import (
	"encoding/json"
	"net/http"
	"fmt"
	"time"
	"bytes"
	"text/template"
	"strings"
)

var (
	CRLF = []byte{'\r', '\n'}
	CRLFSP = []byte{'\r', '\n', ' '}
	loc, _ = time.LoadLocation("Europe/Berlin")
	gpnstart = time.Date(2013, 05, 30, 17, 23, 0, 0, loc)
	gpnstop = time.Date(2013, 06, 02, 15, 30, 0, 0, loc)
	rooms = []string{}
)

func parsegpntime(t string, fallback time.Time) time.Time {
	var year, month, day, hour, min int
	n, err := fmt.Sscanf(t, "%04d%02d%02d-%02d%02d", &year, &month, &day, &hour, &min)
	if err != nil || n != 5 { return fallback }
	return time.Date(year, time.Month(month), day, hour, min, 0, 0, loc)
}

type location struct {
	location string
	ical     []byte
}

func (l location) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	fmt.Println(l.location)
	w.Write(l.ical)
}


type calendar []event
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
	Place       string
}

func (e *event) Starttime() time.Time {
	return parsegpntime(e.Start, gpnstart)
}

func (e *event) Endtime() time.Time {
	return parsegpntime(e.End, gpnstop)
}

func (e *event) Description() string {
	if e.Long_desc != "" {
		return e.Long_desc
	} else if e.Desc != "" {
		return e.Desc
	} else {
		return "No Description"
	}
}

func icaldatetime(t time.Time) string {
	year, month, day := t.UTC().Date()
	hour, min, sec := t.UTC().Clock()
	return fmt.Sprintf("%04d%02d%02dT%02d%02d%02dZ", year, month, day, hour, min, sec)
}

func (e *event) VEVENT() [][]byte {
	lines := [][]byte{}
	lines = append(lines, []byte("BEGIN:VEVENT"))
	lines = append(lines, []byte("DTSAMP:" + icaldatetime(time.Now())))
	lines = append(lines, []byte("DTSTART:" + icaldatetime(e.Starttime())))
	lines = append(lines, []byte("DTEND:" + icaldatetime(e.Endtime())))
	lines = append(lines, []byte("SUMMARY:" + e.Title))
	lines = append(lines, []byte("DESCRIPTION:" + e.Description()))
	lines = append(lines, []byte("LOCATION:" + e.Place))
	lines = append(lines, []byte("END:VEVENT"))

	for i, line := range lines {
		lines[i] = breaklongline(bytes.Replace(line, []byte("\n"), []byte("\\n"), -1))
	}
	return lines
}

func breaklongline(line []byte) []byte {
	var buf bytes.Buffer
	currentlinelength := 0
	for _, char := range bytes.Split(line, []byte{}) {
		if currentlinelength + len(char) > 75 {
			currentlinelength = 0
			buf.Write(CRLFSP)
		}
		currentlinelength += len(char)
		buf.Write(char)
	}
	return buf.Bytes()
}

func (c calendar) ICal() []byte {
	lines := [][]byte{}
	var buf bytes.Buffer
	buf.WriteString("BEGIN:VCALENDAR")
	buf.Write(CRLF)
	buf.WriteString("PRODID:pff")
	buf.Write(CRLF)
	buf.WriteString("VERSION:2.0")
	buf.Write(CRLF)

	for _, e := range c {
		for _, line := range (&e).VEVENT() {
			buf.Write(line)
			buf.Write(CRLF)
		}
	}

	buf.Write(bytes.Join(lines, CRLF))
	buf.Write(CRLF)
	buf.WriteString("END:VCALENDAR")

	return buf.Bytes()
}

const htmltmpl = "" +
`
<head>
<title>Fahrplaene</title>
</head>
<body>
{{range . }}
<a href="/{{.}}">{{.}}</a><br/>
{{end}}
</body>
`

func sync() {
	resp, err := http.Get("http://bl0rg.net/~andi/gpn13-fahrplan.json")
	if err != nil { panic(err) }
	defer resp.Body.Close()

	events := calendar{}
	dec := json.NewDecoder(resp.Body)
	err = dec.Decode(&events)
	if err != nil { panic(err) }

	builder := map[string]calendar{}
	for _, e := range events {
		builder[e.Place] = append(builder[e.Place], e)
	}

	for room, events := range builder {
		if room != "" {
			room = strings.Replace(room, " ", "", -1)
			rooms = append(rooms, room)
			http.Handle("/"+room, location{room, []byte(events.ICal())})
		}
	}
}

func handle(w http.ResponseWriter, r *http.Request) {
	tmpl := template.Must(template.New("html").Parse(htmltmpl))
	tmpl.Execute(w, rooms)
}

func main() {
	sync()
	fmt.Println("starting")
	http.HandleFunc("/", handle)
	if err := http.ListenAndServe(":8000", nil); err != nil {
		panic(err)
	}
}
