package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Konfiguration der Schwellwerte
const (
	sunThreshold = 120.0 // W/m² – Strahlung ab dem eine Stunde als Sonnenstunde zählt
)

// Config enthält die Konfiguration für das Programm
type Config struct {
	LemmyServer    string    `json:"lemmy_server"`
	LemmyCommunity string    `json:"lemmy_community"`
	LemmyUsername  string    `json:"lemmy_username"`
	LemmyPassword  string    `json:"lemmy_password"`
	LemmyToken     string    `json:"lemmy_token"`
	LemmyTokenExp  time.Time `json:"lemmy_token_exp"`

	MastodonServer    string `json:"mastodon_server"`
	MastodonToken     string `json:"mastodon_token"`
	MastodonVisibility string `json:"mastodon_visibility"`
}

// LemmyLoginResponse ist die Antwortstruktur für den Lemmy-Login
type LemmyLoginResponse struct {
	Jwt    string `json:"jwt"`
	UserId int    `json:"id"`
}

type dayStats struct {
	tMax, tMin, rainSum float64
	sunHours           int
}

func getStats(db *sql.DB, loc *time.Location, start, end int64) (dayStats, error) {
	var s dayStats

	// 1) Tagesmax/min
	const qSummary = `
		SELECT MAX(outTemp), MIN(outTemp)
		FROM archive
		WHERE dateTime >= ? AND dateTime < ?;`
	var tMax, tMin sql.NullFloat64
	if err := db.QueryRow(qSummary, start, end).Scan(&tMax, &tMin); err != nil {
		return s, err
	}
	if tMax.Valid {
		s.tMax = tMax.Float64
	} else {
		s.tMax = math.NaN()
		fmt.Fprintf(os.Stderr, "Warnung: MAX(outTemp) ist NULL für Zeitraum %d-%d\n", start, end)
	}
	if tMin.Valid {
		s.tMin = tMin.Float64
	} else {
		s.tMin = math.NaN()
		fmt.Fprintf(os.Stderr, "Warnung: MIN(outTemp) ist NULL für Zeitraum %d-%d\n", start, end)
	}

	// 2) Tagesregenmenge aus archive_day_rain
	const qRain = `SELECT sum FROM archive_day_rain WHERE dateTime >= ? AND dateTime < ? ORDER BY dateTime LIMIT 1;`
	var rainSum sql.NullFloat64
	if err := db.QueryRow(qRain, start, end).Scan(&rainSum); err != nil {
		s.rainSum = 0
		fmt.Fprintf(os.Stderr, "Warnung: Tagesregenmenge (archive_day_rain.sum) ist NULL für Zeitraum %d-%d\n", start, end)
	} else if rainSum.Valid {
		s.rainSum = rainSum.Float64
	} else {
		s.rainSum = 0
		fmt.Fprintf(os.Stderr, "Warnung: Tagesregenmenge (archive_day_rain.sum) ist NULL für Zeitraum %d-%d\n", start, end)
	}

	// 3) Sonnenstunden wie ursprünglich: Zähle Stunden mit maxSolarRad >= sunThreshold
	const qHourly = `
		SELECT dateTime, rain, maxSolarRad
		FROM archive
		WHERE dateTime >= ? AND dateTime < ?;`
	rows, err := db.Query(qHourly, start, end)
	if err != nil {
		return s, err
	}
	defer rows.Close()

	seenSun := make(map[int]struct{})

	for rows.Next() {
		var ts int64
		var rain sql.NullFloat64
		var maxSolarRad sql.NullFloat64
		if err := rows.Scan(&ts, &rain, &maxSolarRad); err != nil {
			return s, err
		}
		h := time.Unix(ts, 0).In(loc).Hour()
		if maxSolarRad.Valid && maxSolarRad.Float64 >= sunThreshold {
			seenSun[h] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return s, err
	}

	s.sunHours = len(seenSun)
	return s, nil
}

// DefaultConfig gibt die Standard-Konfiguration zurück
func DefaultConfig() Config {
	return Config{
		LemmyServer:    "https://natur.23.nu",
		LemmyCommunity: "wetter",
		LemmyUsername:  "wetterbot",
		LemmyPassword:  "CHANGEME",
		LemmyToken:     "",
		LemmyTokenExp:  time.Time{},
		MastodonServer:    "",
		MastodonToken:     "",
		MastodonVisibility: "unlisted",
	}
}

// loadConfig lädt die Konfiguration aus einer JSON-Datei oder erstellt eine Standard-Konfiguration
func loadConfig(configFile string) (Config, error) {
	config := DefaultConfig()

	if configFile != "" {
		data, err := os.ReadFile(configFile)
		if err == nil {
			err = json.Unmarshal(data, &config)
			if err != nil {
				return config, fmt.Errorf("Fehler beim Parsen der Konfigurationsdatei: %v", err)
			}
		}
	}

	return config, nil
}

// saveConfig speichert die Konfiguration in eine JSON-Datei
func saveConfig(config Config, configFile string) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("Fehler beim Marshalling der Konfiguration: %v", err)
	}

	return os.WriteFile(configFile, data, 0644)
}

func lemmyLogin(serverURL, username, password string) (string, error) {
	loginUrl := serverURL + "/api/v3/user/login"
	payload := map[string]string{
		"username_or_email": username,
		"password":          password,
	}
	data, _ := json.Marshal(payload)
	resp, err := http.Post(loginUrl, "application/json", strings.NewReader(string(data)))
	if err != nil {
		return "", fmt.Errorf("Lemmy-Login fehlgeschlagen: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("Fehler beim Lesen der Login-Antwort: %v", err)
	}

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("Lemmy-Login HTTP %d - Antwort: %s", resp.StatusCode, string(body))
	}

	var loginResp LemmyLoginResponse
	if err := json.Unmarshal(body, &loginResp); err != nil {
		return "", fmt.Errorf("Lemmy-Login JSON-Fehler: %v - Antwort: %s", err, string(body))
	}
	return loginResp.Jwt, nil
}

func lemmyGetCommunityID(serverURL, jwt, communityName string) (int, error) {
	url := serverURL + "/api/v3/community?name=" + communityName
	client := &http.Client{}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("Community-GET HTTP %d", resp.StatusCode)
	}
	var respData struct {
		CommunityView struct {
			Community struct {
				Id int `json:"id"`
			} `json:"community"`
		} `json:"community_view"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&respData); err != nil {
		return 0, err
	}
	return respData.CommunityView.Community.Id, nil
}

func lemmyCreatePost(serverURL, jwt string, communityID int, title, body string) error {
	postUrl := serverURL + "/api/v3/post"
	payload := map[string]interface{}{
		"name":         title,
		"body":         body,
		"community_id": communityID,
	}
	data, _ := json.Marshal(payload)
	client := &http.Client{}
	req, err := http.NewRequest("POST", postUrl, strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+jwt)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Post-Erstellung HTTP %d - Antwort: %s", resp.StatusCode, string(body))
	}
	log.Printf("Post erfolgreich erstellt: %s", title)
	return nil
}

// mastodonCreatePost postet einen Status zu Mastodon
func mastodonCreatePost(server, token, text, visibility string) error {
	url := server + "/api/v1/statuses"
	payload := map[string]interface{}{
		"status":     text,
		"visibility": visibility,
	}
	data, _ := json.Marshal(payload)
	client := &http.Client{}
	req, err := http.NewRequest("POST", url, strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Mastodon-Post HTTP %d - Antwort: %s", resp.StatusCode, string(body))
	}
	log.Printf("Post erfolgreich an Mastodon erstellt.")
	return nil
}

// lemmyPostWithRetry versucht einen Post an Lemmy zu senden und wiederholt alle 30 Minuten bei Fehlern
func lemmyPostWithRetry(config Config, title, weatherText string, loopMode bool) {
	const retryInterval = 30 * time.Minute
	const maxRetries = 48 // Maximal 24 Stunden (48 * 30 Minuten) in Loop-Modus
	
	retryCount := 0
	
	for {
		log.Printf("Versuche Post an Lemmy zu senden...")
		
		// Login bei Lemmy
		jwt, err := lemmyLogin(config.LemmyServer, config.LemmyUsername, config.LemmyPassword)
		if err != nil {
			log.Printf("Fehler beim Lemmy-Login: %v", err)
			if loopMode {
				retryCount++
				if retryCount >= maxRetries {
					log.Printf("Maximale Anzahl von Wiederholungen erreicht (%d). Beende Retry-Versuch.", maxRetries)
					return
				}
				log.Printf("Wiederhole in %v... (Versuch %d/%d)", retryInterval, retryCount, maxRetries)
			} else {
				log.Printf("Wiederhole in %v...", retryInterval)
			}
			time.Sleep(retryInterval)
			continue
		}

		// Community-ID holen
		communityID, err := lemmyGetCommunityID(config.LemmyServer, jwt, config.LemmyCommunity)
		if err != nil {
			log.Printf("Fehler beim Holen der Community-ID: %v", err)
			if loopMode {
				retryCount++
				if retryCount >= maxRetries {
					log.Printf("Maximale Anzahl von Wiederholungen erreicht (%d). Beende Retry-Versuch.", maxRetries)
					return
				}
				log.Printf("Wiederhole in %v... (Versuch %d/%d)", retryInterval, retryCount, maxRetries)
			} else {
				log.Printf("Wiederhole in %v...", retryInterval)
			}
			time.Sleep(retryInterval)
			continue
		}

		// Post erstellen
		err = lemmyCreatePost(config.LemmyServer, jwt, communityID, title, weatherText)
		if err != nil {
			log.Printf("Fehler beim Erstellen des Posts: %v", err)
			if loopMode {
				retryCount++
				if retryCount >= maxRetries {
					log.Printf("Maximale Anzahl von Wiederholungen erreicht (%d). Beende Retry-Versuch.", maxRetries)
					return
				}
				log.Printf("Wiederhole in %v... (Versuch %d/%d)", retryInterval, retryCount, maxRetries)
			} else {
				log.Printf("Wiederhole in %v...", retryInterval)
			}
			time.Sleep(retryInterval)
			continue
		}

		log.Printf("Wetterstatistik erfolgreich an Lemmy gepostet!")
		return // Erfolgreich - beende die Schleife
	}
}

func main() {
	// Command line flags
	var testMode = flag.Bool("test", false, "Run in test mode - don't post to Lemmy, just show what would be posted")
	var configFile = flag.String("config", "config.json", "Configuration file path")
	var loopMode = flag.Bool("loop", false, "Run in continuous monitoring mode - posts daily at 4:00 AM")
	flag.Parse()

	if len(flag.Args()) != 1 {
		fmt.Fprintf(os.Stderr, "Usage: %s [flags] /path/to/weewx.sdb\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
		os.Exit(1)
	}
	dbPath := flag.Args()[0]

	// Konfiguration laden
	config, err := loadConfig(*configFile)
	if err != nil {
		log.Fatalf("Fehler beim Laden der Konfiguration: %v", err)
	}

	// Konfiguration speichern (falls sie nicht existierte)
	err = saveConfig(config, *configFile)
	if err != nil {
		log.Printf("Warnung: Konfiguration konnte nicht gespeichert werden: %v", err)
	}

	if *testMode {
		log.Printf("🧪 TEST-MODUS: Keine Posts werden an Lemmy gesendet!")
	}

	if *loopMode {
		log.Printf("🔄 LOOP-MODUS: Starte kontinuierliche Überwachung...")
		log.Printf("Posts werden täglich um 4:00 Uhr erstellt")
		
		// Kontinuierliche Überwachung
		for {
			runWeatherPosting(dbPath, config, *testMode, true)
			
			// Berechne nächsten Lauf um 4:00 Uhr
			now := time.Now()
			nextRun := time.Date(now.Year(), now.Month(), now.Day(), 4, 0, 0, 0, now.Location())
			if now.After(nextRun) {
				nextRun = nextRun.AddDate(0, 0, 1) // Morgen um 4:00 Uhr
			}
			
			sleepDuration := nextRun.Sub(now)
			log.Printf("Nächster Lauf um %s (in %v)", nextRun.Format("02.01.2006 15:04:05"), sleepDuration)
			time.Sleep(sleepDuration)
		}
	} else {
		// Einmalige Ausführung
		runWeatherPosting(dbPath, config, *testMode, false)
	}
}

func runWeatherPosting(dbPath string, config Config, testMode bool, loopMode bool) {
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		log.Fatalf("timezone: %v", err)
	}

	now := time.Now().In(loc)
	yesterday := now.AddDate(0, 0, -1)
	dayBefore := now.AddDate(0, 0, -2)

	startYesterday := time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 0, 0, 0, 0, loc)
	endYesterday := startYesterday.AddDate(0, 0, 1)

	startDayBefore := time.Date(dayBefore.Year(), dayBefore.Month(), dayBefore.Day(), 0, 0, 0, 0, loc)
	endDayBefore := startDayBefore.AddDate(0, 0, 1)

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		log.Fatalf("open DB: %v", err)
	}
	defer db.Close()

	statsY, err := getStats(db, loc, startYesterday.UTC().Unix(), endYesterday.UTC().Unix())
	if err != nil {
		log.Fatalf("yesterday stats: %v", err)
	}
	statsV, err := getStats(db, loc, startDayBefore.UTC().Unix(), endDayBefore.UTC().Unix())
	if err != nil {
		log.Fatalf("vorgestern stats: %v", err)
	}

	// Vor dem Posting: Prüfe auf NaN
	if math.IsNaN(statsY.tMax) || math.IsNaN(statsY.tMin) || math.IsNaN(statsV.tMax) || math.IsNaN(statsV.tMin) {
		log.Printf("Warnung: Ungültige Wetterdaten (NaN) – Posting wird übersprungen!")
		return
	}

	// Wetterstatistik erstellen
	var weatherText = fmt.Sprintf(`Niederschlag: %.1f mm (Vortag: %.1f mm), Sonnenstunden: %d h (Vortag: %d h) Details: https://groloe.wetter.foxel.org/week.html`, 
	statsY.rainSum, statsV.rainSum,
	statsY.sunHours, statsV.sunHours)
	
	// Emojis basierend auf Wetterbedingungen
	var emojis []string
	if statsY.rainSum > 0 {
		emojis = append(emojis, "🌧️ ")
	}
	if statsY.tMax >= 35 {
		emojis = append(emojis, "🏜️ ")
	} else if statsY.tMax >= 30 {
		emojis = append(emojis, "🌡️ ")
	} else if statsY.tMax >= 25 {
		emojis = append(emojis, "☀️ ")
	}
	if statsY.tMin < 0 {
		emojis = append(emojis, "❄️ ")
	}
	if statsY.tMax < 0 {
		emojis = append(emojis, "🧊 ")
	}
	if statsY.tMin >= 20 {
		emojis = append(emojis, "🌙 ")
	}
	
	// Emoji-String erstellen
	emojiString := ""
	if len(emojis) > 0 {
		emojiString = strings.Join(emojis, " ") + " "
	}
	
	title := fmt.Sprintf(`%sWetterstatistik für Overath %s: Temperatur %.1f bis %.1f °C (Vortag: %.1f bis %.1f°C)`, 
		emojiString,
		startYesterday.Format("02.01.2006"),
		statsY.tMax, statsY.tMin, statsV.tMax,
		statsV.tMin)

	// Ausgabe
	fmt.Printf("Statistik für Overath %s: (Vortag)\n", startYesterday.Format("02.01.2006"))
	fmt.Printf("  Höchsttemperatur:   %.1f °C (%.1f °C)\n", statsY.tMax, statsV.tMax)
	fmt.Printf("  Tiefsttemperatur:   %.1f °C (%.1f °C)\n", statsY.tMin, statsV.tMin)
	fmt.Printf("  Niederschlag:       %.1f mm (%.1f mm)\n", statsY.rainSum, statsV.rainSum)
	fmt.Printf("  Sonnenstunden:      %d h (%d h)\n", statsY.sunHours, statsV.sunHours)

	// Lemmy-Posting (nur wenn nicht im Test-Modus)
	if !testMode && config.LemmyPassword != "CHANGEME" {
		lemmyPostWithRetry(config, title, weatherText, loopMode)
	} else if testMode {
		fmt.Printf("\n=== TEST-MODUS: Lemmy-Post würde so aussehen ===\n")
		fmt.Printf("Titel: %s\n", title)
		fmt.Printf("Body:\n%s\n", weatherText)
		fmt.Printf("=== ENDE TEST-MODUS ===\n")
		fmt.Printf("\n=== TEST-MODUS: Mastodon-Konfiguration ===\n")
		fmt.Printf("Server: %s\nToken: %s\nVisibility: %s\n", config.MastodonServer, config.MastodonToken, config.MastodonVisibility)
		fmt.Printf("=== ENDE MASTODON-KONFIG ===\n")
		if config.MastodonServer != "" && config.MastodonToken != "" {
			mastodonText := title + "\n" + weatherText
			fmt.Printf("\n=== TEST-MODUS: Mastodon-Post wird simuliert ===\n")
			fmt.Printf("%s\n", mastodonText)
			fmt.Printf("=== ENDE TEST-MODUS MASTODON ===\n")
			_ = mastodonCreatePost(config.MastodonServer, config.MastodonToken, mastodonText, config.MastodonVisibility)
		}
		return
	} else {
		log.Printf("Lemmy-Posting übersprungen (Passwort nicht konfiguriert)")
	}

	// Mastodon-Posting (optional, unabhängig von Lemmy)
	mastodonErr := error(nil)
	if config.MastodonServer != "" && config.MastodonToken != "" {
		mastodonErr = mastodonCreatePost(config.MastodonServer, config.MastodonToken, title+"\n"+weatherText, config.MastodonVisibility)
		if mastodonErr != nil {
			log.Printf("Fehler beim Mastodon-Post: %v", mastodonErr)
		} else {
			log.Printf("Wetterstatistik erfolgreich an Mastodon gepostet!")
		}
	}
}
