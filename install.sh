#!/bin/bash

# Wetterstatistik Service Installationsskript
# Dieses Skript installiert und konfiguriert den Wetterstatistik-Service als systemd-Service

set -e  # Beende bei Fehlern

# Konstanten
SERVICE_USER="wetterbot"
SERVICE_GROUP="wetterbot"
DB_PATH="/var/lib/weewx/weewx.sdb"

# Prüfe ob das Skript als root ausgeführt wird
check_root() {
    if [ "$EUID" -eq 0 ]; then
        echo "Dieses Skript sollte nicht als root ausgeführt werden!"
        echo "Führe es als normaler Benutzer aus (sudo wird automatisch verwendet)"
        exit 1
    fi
}

# Prüfe ob Go installiert ist
check_go() {
    if ! command -v go &> /dev/null; then
        echo "Go ist nicht installiert!"
        echo "Bitte installiere Go von https://golang.org/dl/"
        exit 1
    fi
    
    echo "Go Version: $(go version)"
}

# Prüfe ob Git installiert ist
check_git() {
    if ! command -v git &> /dev/null; then
        echo "Git ist nicht installiert!"
        echo "Bitte installiere Git: sudo apt-get install git (Ubuntu/Debian) oder sudo yum install git (CentOS/RHEL)"
        exit 1
    fi
    
    echo "Git Version: $(git --version)"
}

# Prüfe ob weewx.sdb existiert
check_weewx_db() {
    if [ ! -f "$DB_PATH" ]; then
        echo "Warnung: weewx.sdb nicht gefunden unter $DB_PATH"
        echo "Bitte stelle sicher, dass weewx installiert ist und die Datenbank existiert"
        echo "Oder passe DB_PATH in diesem Skript an"
        read -p "Möchtest du trotzdem fortfahren? (y/N): " -n 1 -r
        echo
        if [[ ! $REPLY =~ ^[Yy]$ ]]; then
            exit 1
        fi
    else
        echo "weewx.sdb gefunden unter $DB_PATH"
    fi
}

# Erstelle wetterbot Benutzer
create_service_user() {
    echo "Erstelle Service-Benutzer $SERVICE_USER..."
    
    # Prüfe ob Benutzer bereits existiert
    if id "$SERVICE_USER" &>/dev/null; then
        echo "Benutzer $SERVICE_USER existiert bereits"
    else
        # Erstelle Benutzer ohne Login-Shell
        sudo useradd --system --shell /bin/false --create-home --home-dir /home/$SERVICE_USER $SERVICE_USER
        echo "Benutzer $SERVICE_USER erstellt"
    fi
    
    # Prüfe ob Gruppe bereits existiert
    if getent group "$SERVICE_GROUP" &>/dev/null; then
        echo "Gruppe $SERVICE_GROUP existiert bereits"
    else
        # Erstelle Gruppe
        sudo groupadd --system $SERVICE_GROUP
        echo "Gruppe $SERVICE_GROUP erstellt"
    fi
    
    # Füge Benutzer zur Gruppe hinzu
    sudo usermod -a -G $SERVICE_GROUP $SERVICE_USER
    echo "Benutzer $SERVICE_USER zur Gruppe $SERVICE_GROUP hinzugefügt"
}

create_directories() {
    echo "Erstelle Verzeichnisse..."
    
    sudo mkdir -p /opt/wetterstatistik
    sudo mkdir -p /usr/local/bin
    
    # Setze Besitzer auf wetterbot Benutzer
    sudo chown $SERVICE_USER:$SERVICE_GROUP /opt/wetterstatistik
    sudo chmod 755 /opt/wetterstatistik
    
    echo "Verzeichnisse erstellt"
}

compile_program() {
    echo "Kompiliere Programm..."
    
    if [ ! -f "main.go" ]; then
        echo "main.go nicht gefunden! Stelle sicher, dass du im richtigen Verzeichnis bist."
        exit 1
    fi
    
    go build -o daystats main.go
    
    if [ ! -f "daystats" ]; then
        echo "Kompilierung fehlgeschlagen!"
        exit 1
    fi
    
    echo "Programm erfolgreich kompiliert"
}

install_program() {
    echo "Installiere Programm..."

    sudo mv -f daystats /usr/local/bin/wetterstatistik-service
    sudo chown $SERVICE_USER:$SERVICE_GROUP /usr/local/bin/wetterstatistik-service
    sudo chmod +x /usr/local/bin/wetterstatistik-service

    echo "Programm installiert (per mv)"
}

copy_config_files() {
    echo "Kopiere Konfigurationsdateien (nur falls nicht vorhanden)..."

    if [ -f "config.json" ]; then
        if ! sudo test -f /opt/wetterstatistik/config.json; then
            sudo cp config.json /opt/wetterstatistik/
            sudo chown $SERVICE_USER:$SERVICE_GROUP /opt/wetterstatistik/config.json
            sudo chmod 644 /opt/wetterstatistik/config.json
            echo "config.json wurde nach /opt/wetterstatistik/ kopiert."
        else
            echo "/opt/wetterstatistik/config.json existiert bereits, wird nicht überschrieben."
        fi
    else
        echo "config.json nicht gefunden - wird beim ersten Start erstellt"
    fi

    echo "Konfigurationsdateien geprüft"
}

# Erstelle systemd-Service
create_service() {
    echo "Erstelle systemd-Service..."
    
    SERVICE_FILE="/tmp/wetterstatistik-service.service"
    
    cat > $SERVICE_FILE << EOF
[Unit]
Description=Wetterstatistik Service für Overath
After=network.target

[Service]
Type=simple
User=$SERVICE_USER
Group=$SERVICE_GROUP
WorkingDirectory=/opt/wetterstatistik
ExecStart=/usr/local/bin/wetterstatistik-service --loop --config /opt/wetterstatistik/config.json $DB_PATH
Restart=always
RestartSec=10
StandardOutput=journal
StandardError=journal

# Umgebungsvariablen
Environment=GOMAXPROCS=1

# Sicherheitseinstellungen
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/opt/wetterstatistik

[Install]
WantedBy=multi-user.target
EOF
    
    sudo cp $SERVICE_FILE /etc/systemd/system/
    rm $SERVICE_FILE
    
    echo "systemd-Service erstellt"
}

# Aktiviere und starte Service
enable_service() {
    echo "Aktiviere und starte Service..."
    
    sudo systemctl daemon-reload
    sudo systemctl enable wetterstatistik-service
    sudo systemctl start wetterstatistik-service
    
    # Warte kurz und prüfe Status
    sleep 2
    
    if sudo systemctl is-active --quiet wetterstatistik-service; then
        echo "Service erfolgreich gestartet"
        echo "Service-Status: $(sudo systemctl is-active wetterstatistik-service)"
    else
        echo "Service konnte nicht gestartet werden"
        echo "Prüfe die Logs mit: sudo journalctl -u wetterstatistik-service -n 20"
    fi
}

# Hauptfunktion
main() {
    check_root
    check_go
    check_git
    check_weewx_db
    create_service_user
    create_directories
    compile_program
    install_program
    copy_config_files
    create_service
    enable_service
    echo "Installation abgeschlossen!"
    echo "Service läuft als Benutzer: $SERVICE_USER"
    echo "Posts werden täglich um 4:00 Uhr erstellt"
    echo ""
    echo "Verwende folgende Befehle zum Verwalten des Services:"
    echo "  Status: sudo systemctl status wetterstatistik-service"
    echo "  Stoppen: sudo systemctl stop wetterstatistik-service"
    echo "  Starten: sudo systemctl start wetterstatistik-service"
    echo "  Logs: sudo journalctl -u wetterstatistik-service -f"
    echo ""
    echo "Konfiguration:"

    # Service neu starten
    echo "Starte wetterstatistik-service neu..."
    sudo systemctl restart wetterstatistik-service
}

# Skript ausführen
main "$@" 