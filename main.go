package main

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type MembershipDataPoint struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
}

type loweTrackerMembershipPoint struct {
	MemberCount int    `json:"member_count"`
	RecordedAt  string `json:"recorded_at"`
}

type loweTrackerMembershipResponse struct {
	Reform         []loweTrackerMembershipPoint `json:"reform"`
	RestoreBritain []loweTrackerMembershipPoint `json:"restore_britain"`
}

type PartyData struct {
	Name   string                `json:"name"`
	Color  string                `json:"color"`
	Points []MembershipDataPoint `json:"points"`
}

type TemplateData struct {
	GreenDataJSON          template.JS
	ReformDataJSON         template.JS
	RestoreBritainDataJSON template.JS
	HomeSchemaJSON         template.JS
	LastUpdated            string
	GreenLatest            int
	ReformLatest           int
	RestoreBritainLatest   int
	GreenLatestDateLabel   string
	GreenLatestDateISO     string
}

func formatNumber(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}

	var result []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}
	return string(result)
}

func getGreenPartyData() []MembershipDataPoint {
	raw := []struct {
		date  time.Time
		count int
	}{
		{time.Date(2023, 12, 1, 0, 0, 0, 0, time.UTC), 53000},
		{time.Date(2024, 6, 16, 0, 0, 0, 0, time.UTC), 55000},
		{time.Date(2025, 3, 18, 0, 0, 0, 0, time.UTC), 60000},
		{time.Date(2025, 9, 2, 0, 0, 0, 0, time.UTC), 68500},
		{time.Date(2025, 9, 9, 0, 0, 0, 0, time.UTC), 70000},
		{time.Date(2025, 9, 11, 0, 0, 0, 0, time.UTC), 72000},
		{time.Date(2025, 9, 18, 0, 0, 0, 0, time.UTC), 73000},
		{time.Date(2025, 9, 20, 0, 0, 0, 0, time.UTC), 77000},
		{time.Date(2025, 9, 24, 0, 0, 0, 0, time.UTC), 79000},
		{time.Date(2025, 10, 6, 0, 0, 0, 0, time.UTC), 86000},
		{time.Date(2025, 10, 8, 0, 0, 0, 0, time.UTC), 93000},
		{time.Date(2025, 10, 14, 0, 0, 0, 0, time.UTC), 110000},
		{time.Date(2025, 10, 19, 0, 0, 0, 0, time.UTC), 129000},
		{time.Date(2025, 10, 22, 0, 0, 0, 0, time.UTC), 140000},
		{time.Date(2025, 12, 9, 0, 0, 0, 0, time.UTC), 180000},
		{time.Date(2026, 1, 26, 0, 0, 0, 0, time.UTC), 190000},
		{time.Date(2026, 2, 18, 0, 0, 0, 0, time.UTC), 195000},
		{time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), 200000},
		{time.Date(2026, 3, 6, 0, 0, 0, 0, time.UTC), 215000},
		{time.Date(2026, 3, 6, 15, 21, 0, 0, time.UTC), 216000},
	}

	sort.Slice(raw, func(i, j int) bool {
		return raw[i].date.Before(raw[j].date)
	})

	points := make([]MembershipDataPoint, len(raw))
	for i, r := range raw {
		points[i] = MembershipDataPoint{
			Date:  r.date.Format("2006-01-02"),
			Count: r.count,
		}
	}

	// Deduplicate: keep the latest value for each date
	seen := make(map[string]int)
	var deduped []MembershipDataPoint
	for _, p := range points {
		if idx, ok := seen[p.Date]; ok {
			deduped[idx] = p
		} else {
			seen[p.Date] = len(deduped)
			deduped = append(deduped, p)
		}
	}

	return deduped
}

func loadCSV(path string) ([]MembershipDataPoint, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	reader := csv.NewReader(f)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}

	var points []MembershipDataPoint
	for _, row := range records {
		if len(row) < 2 {
			continue
		}
		count, err := strconv.Atoi(row[0])
		if err != nil {
			continue
		}
		points = append(points, MembershipDataPoint{Date: row[1], Count: count})
	}
	return points, nil
}

func normaliseRemoteMembershipData(points []loweTrackerMembershipPoint) []MembershipDataPoint {
	sort.Slice(points, func(i, j int) bool {
		return points[i].RecordedAt < points[j].RecordedAt
	})

	seen := make(map[string]int)
	var normalised []MembershipDataPoint

	for _, point := range points {
		recordedAt, err := time.Parse(time.RFC3339Nano, point.RecordedAt)
		if err != nil {
			recordedAt, err = time.Parse(time.RFC3339, point.RecordedAt)
			if err != nil {
				continue
			}
		}

		date := recordedAt.UTC().Format("2006-01-02")
		normalisedPoint := MembershipDataPoint{
			Date:  date,
			Count: point.MemberCount,
		}

		if idx, ok := seen[date]; ok {
			normalised[idx] = normalisedPoint
		} else {
			seen[date] = len(normalised)
			normalised = append(normalised, normalisedPoint)
		}
	}

	return normalised
}

func getLoweTrackerData() ([]MembershipDataPoint, []MembershipDataPoint, error) {
	req, err := http.NewRequest(http.MethodGet, "https://lowetracker.co.uk/api/reform/membership", nil)
	if err != nil {
		return nil, nil, fmt.Errorf("creating lowe tracker request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "greenpartytracker.co.uk/1.0")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("fetching lowe tracker data: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("fetching lowe tracker data: unexpected status %s", resp.Status)
	}

	var payload loweTrackerMembershipResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, nil, fmt.Errorf("decoding lowe tracker data: %w", err)
	}

	return normaliseRemoteMembershipData(payload.Reform), normaliseRemoteMembershipData(payload.RestoreBritain), nil
}

func getReformData(dbPath string) ([]MembershipDataPoint, error) {
	db, err := sql.Open("sqlite3", dbPath+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("opening reform db: %w", err)
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT member_count, DATE(recorded_at) as d
		FROM reform_membership
		GROUP BY d
		ORDER BY d
	`)
	if err != nil {
		return nil, fmt.Errorf("querying reform membership: %w", err)
	}
	defer rows.Close()

	var points []MembershipDataPoint
	for rows.Next() {
		var count int
		var date string
		if err := rows.Scan(&count, &date); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}
		points = append(points, MembershipDataPoint{Date: date, Count: count})
	}
	return points, rows.Err()
}

func getRestoreBritainData(dbPath string) ([]MembershipDataPoint, error) {
	db, err := sql.Open("sqlite3", dbPath+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("opening restore britain db: %w", err)
	}
	defer db.Close()

	rows, err := db.Query(`
		SELECT MAX(member_count), DATE(recorded_at) as d
		FROM restore_britain_membership
		GROUP BY d
		ORDER BY d
	`)
	if err != nil {
		return nil, fmt.Errorf("querying restore britain membership: %w", err)
	}
	defer rows.Close()

	var points []MembershipDataPoint
	for rows.Next() {
		var count int
		var date string
		if err := rows.Scan(&count, &date); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}
		points = append(points, MembershipDataPoint{Date: date, Count: count})
	}
	return points, rows.Err()
}

func buildHomeSchema(greenLatest int, greenLatestDateLabel, greenLatestDateISO string) template.JS {
	if greenLatestDateLabel == "" {
		greenLatestDateLabel = "the latest update"
	}
	if greenLatestDateISO == "" {
		greenLatestDateISO = time.Now().Format("2006-01-02")
	}

	description := fmt.Sprintf(
		"Track Green Party membership numbers in the UK. Latest recorded figure: %s members on %s.",
		formatNumber(greenLatest),
		greenLatestDateLabel,
	)

	faq := []map[string]any{
		{
			"@type": "Question",
			"name":  "What are the latest Green Party membership numbers?",
			"acceptedAnswer": map[string]any{
				"@type": "Answer",
				"text":  fmt.Sprintf("The latest recorded Green Party membership number on this tracker is %s members, recorded on %s.", formatNumber(greenLatest), greenLatestDateLabel),
			},
		},
		{
			"@type": "Question",
			"name":  "Where do these Green Party membership numbers come from?",
			"acceptedAnswer": map[string]any{
				"@type": "Answer",
				"text":  "This site compiles Green Party membership figures from official announcements, press releases, and other verified public reports.",
			},
		},
		{
			"@type": "Question",
			"name":  "Does this site show Green Party membership history over time?",
			"acceptedAnswer": map[string]any{
				"@type": "Answer",
				"text":  "Yes. The tracker includes historical Green Party membership numbers so visitors can follow growth over time and compare it with other UK parties.",
			},
		},
	}

	schema := map[string]any{
		"@context": "https://schema.org",
		"@graph": []any{
			map[string]any{
				"@type":       "WebSite",
				"@id":         "https://greenpartytracker.co.uk/#website",
				"url":         "https://greenpartytracker.co.uk",
				"name":        "Green Party Membership Numbers",
				"description": "Live UK tracker of Green Party membership numbers, history, and growth.",
				"inLanguage":  "en-GB",
			},
			map[string]any{
				"@type":        "WebPage",
				"@id":          "https://greenpartytracker.co.uk/#webpage",
				"url":          "https://greenpartytracker.co.uk",
				"name":         "Green Party Membership Numbers | Live UK Tracker & History",
				"isPartOf":     map[string]any{"@id": "https://greenpartytracker.co.uk/#website"},
				"about":        map[string]any{"@id": "https://greenpartytracker.co.uk/#dataset"},
				"description":  description,
				"dateModified": greenLatestDateISO,
				"inLanguage":   "en-GB",
			},
			map[string]any{
				"@type":                "Dataset",
				"@id":                  "https://greenpartytracker.co.uk/#dataset",
				"name":                 "Green Party membership numbers",
				"description":          "Historic and latest Green Party membership numbers in the UK, updated from public announcements and verified reports.",
				"url":                  "https://greenpartytracker.co.uk",
				"dateModified":         greenLatestDateISO,
				"temporalCoverage":     "2023-12-01/" + greenLatestDateISO,
				"inLanguage":           "en-GB",
				"measurementTechnique": "Membership figures compiled from official announcements, press releases, and verified reports.",
				"variableMeasured":     "Green Party membership numbers",
				"keywords": []string{
					"green party membership numbers",
					"green party membership tracker",
					"green party membership history",
					"green party members",
				},
			},
			map[string]any{
				"@type":      "FAQPage",
				"@id":        "https://greenpartytracker.co.uk/#faq",
				"mainEntity": faq,
			},
		},
	}

	schemaJSON, err := json.Marshal(schema)
	if err != nil {
		return template.JS("{}")
	}

	return template.JS(schemaJSON)
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8095"
	}

	reformDBPath := os.Getenv("REFORM_DB_PATH")
	if reformDBPath == "" {
		reformDBPath = "/mnt/games/git/xrank/xrank.db"
	}

	funcMap := template.FuncMap{
		"formatNumber": formatNumber,
	}

	tmpl, err := template.New("index.html").Funcs(funcMap).ParseFiles("templates/index.html")
	if err != nil {
		log.Fatalf("Failed to parse index template: %v", err)
	}

	wealthTmpl, err := template.New("wealth.html").Funcs(funcMap).ParseFiles("templates/wealth.html")
	if err != nil {
		log.Fatalf("Failed to parse wealth template: %v", err)
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}

		greenData := getGreenPartyData()
		reformData, restoreBritainData, err := getLoweTrackerData()
		if err != nil {
			log.Printf("Lowe Tracker data unavailable (%v), trying local fallback sources", err)

			reformData, err = getReformData(reformDBPath)
			if err != nil {
				log.Printf("SQLite reform data unavailable (%v), trying CSV fallback", err)
				reformData, _ = loadCSV("data/reform.csv")
			}

			restoreBritainData, err = getRestoreBritainData(reformDBPath)
			if err != nil {
				log.Printf("SQLite restore britain data unavailable (%v), trying CSV fallback", err)
				restoreBritainData, _ = loadCSV("data/restore_britain.csv")
			}
		}

		greenJSON, _ := json.Marshal(greenData)
		reformJSON, _ := json.Marshal(reformData)
		restoreBritainJSON, _ := json.Marshal(restoreBritainData)

		var greenLatest, reformLatest, restoreBritainLatest int
		var greenLatestDateLabel, greenLatestDateISO string
		if len(greenData) > 0 {
			latestPoint := greenData[len(greenData)-1]
			greenLatest = latestPoint.Count
			greenLatestDateISO = latestPoint.Date

			if latestDate, err := time.Parse("2006-01-02", latestPoint.Date); err == nil {
				greenLatestDateLabel = latestDate.Format("2 January 2006")
			} else {
				greenLatestDateLabel = latestPoint.Date
			}
		}
		if len(reformData) > 0 {
			reformLatest = reformData[len(reformData)-1].Count
		}
		if len(restoreBritainData) > 0 {
			restoreBritainLatest = restoreBritainData[len(restoreBritainData)-1].Count
		}

		data := TemplateData{
			GreenDataJSON:          template.JS(greenJSON),
			ReformDataJSON:         template.JS(reformJSON),
			RestoreBritainDataJSON: template.JS(restoreBritainJSON),
			HomeSchemaJSON:         buildHomeSchema(greenLatest, greenLatestDateLabel, greenLatestDateISO),
			LastUpdated:            time.Now().Format("2 January 2006, 15:04"),
			GreenLatest:            greenLatest,
			ReformLatest:           reformLatest,
			RestoreBritainLatest:   restoreBritainLatest,
			GreenLatestDateLabel:   greenLatestDateLabel,
			GreenLatestDateISO:     greenLatestDateISO,
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := tmpl.Execute(w, data); err != nil {
			log.Printf("Template execution error: %v", err)
			http.Error(w, "Internal Server Error", 500)
		}
	})

	http.HandleFunc("/api/data", func(w http.ResponseWriter, r *http.Request) {
		greenData := getGreenPartyData()
		reformData, restoreBritainData, err := getLoweTrackerData()
		if err != nil {
			reformData, err = getReformData(reformDBPath)
			if err != nil {
				reformData, _ = loadCSV("data/reform.csv")
			}

			restoreBritainData, err = getRestoreBritainData(reformDBPath)
			if err != nil {
				restoreBritainData, _ = loadCSV("data/restore_britain.csv")
			}
		}

		resp := map[string]interface{}{
			"green":           greenData,
			"reform":          reformData,
			"restore_britain": restoreBritainData,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	http.HandleFunc("/wealth", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := wealthTmpl.Execute(w, nil); err != nil {
			log.Printf("Wealth template execution error: %v", err)
			http.Error(w, "Internal Server Error", 500)
		}
	})

	http.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, "User-agent: *\nAllow: /\n\nSitemap: https://greenpartytracker.co.uk/sitemap.xml\n")
	})

	http.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, r *http.Request) {
		greenLatestDateISO := time.Now().Format("2006-01-02")
		if greenData := getGreenPartyData(); len(greenData) > 0 {
			greenLatestDateISO = greenData[len(greenData)-1].Date
		}

		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url>
    <loc>https://greenpartytracker.co.uk/</loc>
    <lastmod>%s</lastmod>
  </url>
  <url>
    <loc>https://greenpartytracker.co.uk/wealth</loc>
  </url>
</urlset>`, greenLatestDateISO)
	})

	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	log.Printf("🌿 Green Party Tracker starting on http://localhost:%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
