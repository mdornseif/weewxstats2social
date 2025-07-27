# Wetterstatistik Service für Overath

Ein Go-Programm zur automatischen Erstellung und Veröffentlichung von Wetterstatistiken aus einer weewx-Datenbank. Das Programm erstellt täglich um 4:00 Uhr einen Post mit den Wetterdaten des Vortags und veröffentlicht ihn auf einem Lemmy-Server.

## Funktionen

- **Automatische Wetterstatistik**: Erstellt täglich Statistiken aus der weewx-Datenbank
- **Lemmy-Integration**: Veröffentlicht Posts automatisch auf einem Lemmy-Server
- **Service-Betrieb**: Läuft als systemd-Service mit automatischem Neustart
- **Konfigurierbar**: Einstellungen über JSON-Datei
- **Test-Modus**: Zum Testen ohne tatsächliches Posting
- **Vergleichsdaten**: Zeigt immer auch die Daten des Vortags zum Vergleich
- **Mastodon-Integration**: Wenn konfiguriert, wird die Wetterstatistik zusätzlich auf Mastodon gepostet (kein Retry, Fehler werden geloggt)

## Wetterdaten

Das Programm erstellt Statistiken für:
- **Temperatur**: Höchst- und Tiefsttemperatur
- **Niederschlag**: Gesamtniederschlag in mm
- **Sonnenstunden**: Stunden mit Strahlung ≥ 120 W/m²

## Schnellinstallation

Für eine automatische Installation steht ein Installationsskript zur Verfügung:

```bash
bash ./install.sh
```

**Voraussetzungen:**
- Go 1.19 oder höher
- Git
- weewx installiert mit Datenbank unter `/var/lib/weewx/weewx.sdb`
- sudo-Rechte

## Manuelle Installation

1. **Abhängigkeiten installieren:**
   ```bash
   go mod tidy
   ```

2. **Programm kompilieren:**
   ```bash
   go build -o daystats main.go
   ```

3. **Konfiguration erstellen:**
   ```bash
   ./daystats /var/lib/weewx/weewx.sdb
   ```
   Dies erstellt eine `config.json` Datei mit Standardwerten.

4. **Konfiguration anpassen:**
   ```bash
   nano config.json
   ```
   
   Beispiel-Konfiguration:
   ```json
   {
     "lemmy_server": "https://natur.23.nu",
     "lemmy_community": "wetter",
     "lemmy_username": "wetterbot",
     "lemmy_password": "DEIN_PASSWORT",
     "lemmy_token": "",
     "lemmy_token_exp": "0001-01-01T00:00:00Z",
     "mastodon_server": "https://mastodon.social",
     "mastodon_token": "DEIN_MASTODON_TOKEN",
     "mastodon_visibility": "unlisted"
   }
   ```

## Verwendung

### Einmalige Ausführung
```bash
./daystats /var/lib/weewx/weewx.sdb
```

### Test-Modus (zeigt nur, was gepostet würde)
```bash
./daystats -test /var/lib/weewx/weewx.sdb
```

### Kontinuierlicher Betrieb (täglich um 4:00 Uhr)
```bash
./daystats -loop /var/lib/weewx/weewx.sdb
```

### Mit benutzerdefinierter Konfigurationsdatei
```bash
./daystats -config /pfad/zur/config.json /var/lib/weewx/weewx.sdb
```

## Konfiguration

Die Konfigurationsdatei `config.json` enthält:

- `lemmy_server`: URL des Lemmy-Servers
- `lemmy_community`: Name der Community
- `lemmy_username`: Benutzername für Lemmy
- `lemmy_password`: Passwort für Lemmy
- `lemmy_token`: JWT-Token (wird automatisch verwaltet)
- `lemmy_token_exp`: Token-Ablaufzeit (wird automatisch verwaltet)
- `mastodon_server`: URL des Mastodon-Servers (optional)
- `mastodon_token`: Zugangstoken für Mastodon (optional)
- `mastodon_visibility`: Sichtbarkeit des Mastodon-Posts (z.B. `unlisted`, `public`, `private`, `direct`) (optional, Standard: `unlisted`)

## Schwellwerte

Das Programm verwendet folgende Schwellwerte:
- **Sonnenstunden**: 120 W/m² Strahlung
- **Regenstunden**: 0.1 mm Niederschlag (entfernt)

## Beispiel-Ausgabe

```
Statistik für Overath 25.06.2025: (Vortag)
  Höchsttemperatur:   29.2 °C (22.4 °C)
  Tiefsttemperatur:   19.3 °C (10.7 °C)
  Niederschlag:       0.0 mm (0.0 mm)
  Sonnenstunden:      14 h (15 h)
```

