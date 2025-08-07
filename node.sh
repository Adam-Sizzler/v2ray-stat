#!/usr/bin/env bash

REPO="Adam-Sizzler/v2ray-stat"
FILE="v2ray-stat-node-linux-amd64"
DEST_DIR="/usr/local/etc/v2ray-stat"
LOG_DIR="/opt/xcore"
LOG_FILE="${LOG_DIR}/cron_jobs.log"
SERVICE_FILE="/etc/systemd/system/v2ray-stat-node.service"

# Создание директорий, если они не существуют
echo "$(date): Creating directories if they don't exist" >> "$LOG_FILE"
mkdir -p "$DEST_DIR" "$LOG_DIR" || {
  echo "$(date): Error: Failed to create directories $DEST_DIR or $LOG_DIR" >> "$LOG_FILE"
  echo >> "$LOG_FILE"
  exit 1
}

# Получение URL последнего релиза (включая пререлизы)
echo "$(date): Starting download of $FILE" >> "$LOG_FILE"
URL=$(curl -s https://api.github.com/repos/$REPO/releases | \
  jq -r '.[] | select(.prerelease == true or .prerelease == false) | .assets[] | select(.name == "'"$FILE"'") | .browser_download_url' | \
  head -1)

if [ -z "$URL" ]; then
  echo "$(date): Error: File $FILE not found in any release" >> "$LOG_FILE"
  echo >> "$LOG_FILE"
  exit 1
fi

# Остановка службы, если она существует
echo "$(date): Stopping v2ray-stat-node service..." >> "$LOG_FILE"
systemctl stop v2ray-stat-node.service || echo "$(date): Warning: Failed to stop v2ray-stat-node service" >> "$LOG_FILE"

# Скачивание и установка исполняемого файла
echo "$(date): Downloading $FILE to $DEST_DIR..." >> "$LOG_FILE"
curl -L -o "$DEST_DIR/v2ray-stat-node" "$URL" && chmod +x "$DEST_DIR/v2ray-stat-node" || {
  echo "$(date): Error: Failed to download or set executable permissions for $FILE" >> "$LOG_FILE"
  echo >> "$LOG_FILE"
  exit 1
}

# Создание/перезапись файла службы
echo "$(date): Creating or updating $SERVICE_FILE..." >> "$LOG_FILE"
cat > "$SERVICE_FILE" << EOF
[Unit]
Description=V2ray Stat Node Service
After=network.target

[Service]
User=root
Group=root
ExecStart=$DEST_DIR/v2ray-stat-node
WorkingDirectory=$DEST_DIR
Restart=always
RestartSec=5
KillSignal=SIGTERM
TimeoutStopSec=10

[Install]
WantedBy=multi-user.target
EOF

if [ -z "$URL" ]; then
  echo "$(date): Error: File $FILE not found in any release" >> "$LOG_FILE"
  echo >> "$LOG_FILE"
  exit 1
fi

systemctl daemon-reload
systemctl enable v2ray-stat-node.service
systemctl restart v2ray-stat-node.service

echo "$(date): Done! $FILE downloaded to $DEST_DIR and set as executable, service configured" >> "$LOG_FILE"
echo >> "$LOG_FILE"