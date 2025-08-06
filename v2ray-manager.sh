#!/usr/bin/env bash

# Copyright (c) 2025 xCore Authors
# This file is part of xCore.
# xCore is licensed under the xCore Software License. See the LICENSE file for details.

###################################
### GLOBAL CONSTANTS AND VARIABLES
###################################
VERSION_MANAGER='25.7.29'

DIR_XCORE="/opt/xcore"
DIR_XRAY="/usr/local/etc/xray"
DIR_HAPROXY="/etc/haproxy"

REPO_URL="https://github.com/cortez24rus/XCore/archive/refs/heads/main.tar.gz"

###################################
### INITIALIZATION AND DECLARATIONS
###################################
declare -A defaults
declare -A args
declare -A regex
declare -A generate

###################################
### REGEX PATTERNS FOR VALIDATION
###################################
regex[domain]="^([a-zA-Z0-9-]+)\.([a-zA-Z0-9-]+\.[a-zA-Z]{2,})$"
regex[port]="^[1-9][0-9]*$"
regex[username]="^[a-zA-Z0-9]+$"
regex[ipv4]="^([0-9]{1,3}\.){3}[0-9]{1,3}$"
regex[tgbot_token]="^[0-9]{8,10}:[a-zA-Z0-9_-]{35}$"
regex[tgbot_admins]="^[a-zA-Z][a-zA-Z0-9_]{4,31}(,[a-zA-Z][a-zA-Z0-9_]{4,31})*$"
regex[domain_port]="^[a-zA-Z0-9]+([-.][a-zA-Z0-9]+)*\.[a-zA-Z]{2,}(:[1-9][0-9]*)?$"
regex[file_path]="^[a-zA-Z0-9_/.-]+$"
regex[url]="^(http|https)://([a-zA-Z0-9.-]+\.[a-zA-Z]{2,})(:[0-9]{1,5})?(/.*)?$"
generate[path]="tr -dc 'A-Za-z0-9' < /dev/urandom | head -c 30"

###################################
### OUTPUT FORMATTING FUNCTIONS
###################################
out_data()   { echo -e "\e[1;33m$1\033[0m \033[1;37m$2\033[0m"; }
tilda()      { echo -e "\033[31m\033[38;5;214m$*\033[0m"; }
warning()    { echo -e "\033[31m [!]\033[38;5;214m$*\033[0m"; }
error()      { echo -e "\033[31m\033[01m$*\033[0m"; exit 1; }
info()       { echo -e "\033[32m\033[01m$*\033[0m"; }
question()   { echo -e "\033[32m[?]\e[1;33m$*\033[0m"; }
hint()       { echo -e "\033[33m\033[01m$*\033[0m"; }
reading()    { read -rp " $(question "$1")" "$2"; }
text()       { eval echo "\${${LANGUAGE}[$*]}"; }
text_eval()  { eval echo "\$(eval echo "\${${LANGUAGE}[$*]}")"; }


###################################
### LANGUAGE STRINGS
###################################
EU[0]="Language:\n  1. English (default) \n  2. Русский"
RU[0]="Язык:\n  1. English (по умолчанию) \n  2. Русский"
EU[1]="Choose an action:"
RU[1]="Выбери действие:"
EU[2]="Error: this script requires superuser (root) privileges to run."
RU[2]="Ошибка: для выполнения этого скрипта необходимы права суперпользователя (root)."
EU[3]="Unable to determine IP address."
RU[3]="Не удалось определить IP-адрес."
EU[4]="Reinstalling script..."
RU[4]="Повторная установка скрипта..."
EU[5]="WARNING!"
RU[5]="ВНИМАНИЕ!"
EU[6]="It is recommended to perform the following actions before running the script"
RU[6]="Перед запуском скрипта рекомендуется выполнить следующие действия"
EU[7]="Annihilation of the system!"
RU[7]="Аннигиляция системы!"

EU[9]="CANCEL"
RU[9]="ОТМЕНА"
EU[10]="\n|--------------------------------------------------------------------------|\n"
RU[10]="\n|--------------------------------------------------------------------------|\n"
EU[11]="Enter username:"
RU[11]="Введите имя пользователя:"
EU[12]="Enter user password:"
RU[12]="Введите пароль пользователя:"
EU[13]="Enter your domain A record:"
RU[13]="Введите доменную запись типа A:"
EU[14]="Error: the entered address '$temp_value' is incorrectly formatted."
RU[14]="Ошибка: введённый адрес '$temp_value' имеет неверный формат."
EU[15]="Enter your email registered with Cloudflare:"
RU[15]="Введите вашу почту, зарегистрированную на Cloudflare:"
EU[16]="Enter your Cloudflare API token (Edit zone DNS) or global API key:"
RU[16]="Введите ваш API токен Cloudflare (Edit zone DNS) или Cloudflare global API key:"
EU[17]="Verifying domain, API token/key, and email..."
RU[17]="Проверка домена, API токена/ключа и почты..."
EU[18]="Error: invalid domain, API token/key, or email. Please try again."
RU[18]="Ошибка: неправильно введён домен, API токен/ключ или почта. Попробуйте снова."

EU[20]="Error: failed to connect to WARP. Manual acceptance of the terms of service is required."
RU[20]="Ошибка: не удалось подключиться к WARP. Требуется вручную согласиться с условиями использования."
EU[21]="Access link to node exporter:"
RU[21]="Доступ по ссылке к node exporter:"
EU[22]="Access link to shell in a box:"
RU[22]="Доступ по ссылке к shell in a box:"
EU[23]="Creating a backup and rotation."
RU[23]="Создание резевной копии и ротация."
EU[24]="Enter Node Exporter path:"
RU[24]="Введите путь к Node Exporter:"

EU[27]="Enter subscription path:"
RU[27]="Введите путь к подписке:"

EU[29]="Error: path cannot be empty, please re-enter."
RU[29]="Ошибка: путь не может быть пустым, повторите ввод."
EU[30]="Error: path must not contain characters {, }, /, $, \\, please re-enter."
RU[30]="Ошибка: путь не должен содержать символы {, }, /, $, \\, повторите ввод."

EU[33]="Error: invalid choice, please try again."
RU[33]="Ошибка: неверный выбор, попробуйте снова."

EU[36]="Updating system and installing necessary packages."
RU[36]="Обновление системы и установка необходимых пакетов."
EU[37]="Configuring Haproxy."
RU[37]="Настройка Haproxy."
EU[38]="Download failed, retrying..."
RU[38]="Скачивание не удалось, пробуем снова..."
EU[39]="Adding user."
RU[39]="Добавление пользователя."
EU[40]="Enabling automatic security updates."
RU[40]="Автоматическое обновление безопасности."
EU[41]="Enabling BBR."
RU[41]="Включение BBR."
EU[42]="Disabling IPv6."
RU[42]="Отключение IPv6."
EU[43]="Configuring WARP."
RU[43]="Настройка WARP."
EU[44]="Issuing certificates."
RU[44]="Выдача сертификатов."
EU[45]="Configuring NGINX."
RU[45]="Настройка NGINX."
EU[46]="Setting Xray."
RU[46]="Настройка Xray."
EU[47]="Configuring UFW."
RU[47]="Настройка UFW."
EU[48]="Configuring SSH."
RU[48]="Настройка SSH."
EU[49]="Generate a key for your OS (ssh-keygen)."
RU[49]="Сгенерируйте ключ для своей ОС (ssh-keygen)."
EU[50]="In Windows, install the openSSH package and enter the command in PowerShell (recommended to research key generation online)."
RU[50]="В Windows нужно установить пакет openSSH и ввести команду в PowerShell (рекомендуется изучить генерацию ключей в интернете)."
EU[51]="If you are on Linux, you probably know what to do C:"
RU[51]="Если у вас Linux, то вы сами все умеете C:"
EU[52]="Command for Windows:"
RU[52]="Команда для Windows:"
EU[53]="Command for Linux:"
RU[53]="Команда для Linux:"
EU[54]="Configure SSH (optional step)? [y/N]:"
RU[54]="Настроить SSH (необязательный шаг)? [y/N]:"
EU[55]="Error: Keys not found. Please add them to the server before retrying..."
RU[55]="Ошибка: ключи не найдены, добавьте его на сервер, прежде чем повторить..."
EU[56]="Key found, proceeding with SSH setup."
RU[56]="Ключ найден, настройка SSH."
EU[57]="Client-side configuration."
RU[57]="Настройка клиентской части."
EU[58]="SAVE THIS SCREEN!"
RU[58]="СОХРАНИ ЭТОТ ЭКРАН!"
EU[59]="Subscription page link:"
RU[59]="Ссылка на страницу подписки:"

EU[62]="SSH connection:"
RU[62]="Подключение по SSH:"
EU[63]="Username:"
RU[63]="Имя пользователя:"
EU[64]="Password:"
RU[64]="Пароль:"
EU[65]="Log file path:"
RU[65]="Путь к лог файлу:"
EU[66]="Prometheus monitor."
RU[66]="Мониторинг Prometheus."

EU[70]="Secret key:"
RU[70]="Секретный ключ:"
EU[71]="Current operating system is \$SYS.\\\n The system lower than \$SYSTEM \${MAJOR[int]} is not supported. Feedback: [https://github.com/cortez24rus/xcore/issues]"
RU[71]="Текущая операционная система: \$SYS.\\\n Система с версией ниже, чем \$SYSTEM \${MAJOR[int]}, не поддерживается. Обратная связь: [https://github.com/cortez24rus/xcore/issues]"
EU[72]="Install dependence-list:"
RU[72]="Список зависимостей для установки:"
EU[73]="All dependencies already exist and do not need to be installed additionally."
RU[73]="Все зависимости уже установлены и не требуют дополнительной установки."
EU[74]="OS - $SYS"
RU[74]="OS - $SYS"
EU[75]="Invalid option for --$key: $value. Use 'true' or 'false'."
RU[75]="Неверная опция для --$key: $value. Используйте 'true' или 'false'."
EU[76]="Unknown option: $1"
RU[76]="Неверная опция: $1"
EU[79]="Configuring site template."
RU[79]="Настройка шаблона сайта."
EU[80]="Random template name:"
RU[80]="Случайное имя шаблона:"
EU[81]="Enter your domain CNAME record:"
RU[81]="Введите доменную запись типа CNAME:"
EU[82]="Enter Shell in a box path:"
RU[82]="Введите путь к Shell in a box:"
EU[83]="Terminal emulator Shell in a box."
RU[83]="Эмулятор терминала Shell in a box."

EU[84]="0. Previous menu"
RU[84]="0. Предыдущее меню"
EU[85]="Press Enter to return to the menu..."
RU[85]="Нажмите Enter, чтобы вернуться в меню..."
EU[86]="X Core $VERSION_MANAGER"
RU[86]="X Core $VERSION_MANAGER"
EU[87]="1. Perform standard installation"
RU[87]="1. Выполнить стандартную установку"
EU[88]="2. Restore from backup"
RU[88]="2. Восстановить из резервной копии"
EU[89]="3. Change proxy domain name"
RU[89]="3. Изменить доменное имя прокси"
EU[90]="4. Reissue SSL certificates"
RU[90]="4. Перевыпустить SSL-сертификаты"
EU[91]="5. Copy website to server"
RU[91]="5. Скопировать веб-сайт на сервер"
EU[92]="6. Show directory size"
RU[92]="6. Показать размер директории"
EU[93]="7. Show traffic statistics"
RU[93]="7. Показать статистику трафика"
EU[94]="8. Update Xray core"
RU[94]="8. Обновить ядро Xray"
EU[95]="X. Manage Xray core"
RU[95]="X. Управлять ядром Xray"
EU[96]="12. Change interface language"
RU[96]="12. Изменить язык интерфейса"
EU[97]="Client migration initiation (experimental feature)."
RU[97]="Начало миграции клиентов (экспериментальная функция)."
EU[98]="Client migration is complete."
RU[98]="Миграция клиентов завершена."
EU[99]="Settings custom JSON subscription."
RU[99]="Настройки пользовательской JSON-подписки."
EU[100]="Restore from backup."
RU[100]="Восстановление из резервной копии."
EU[101]="Backups:"
RU[101]="Резервные копии:"
EU[102]="Enter the number of the archive to restore:"
RU[102]="Введите номер архива для восстановления:"
EU[103]="Restoration is complete."
RU[103]="Восстановление завершено."
EU[104]="Selected archive:"
RU[104]="Выбран архив:"
EU[105]="Traffic statistics:\n  1. By years \n  2. By months \n  3. By days \n  4. By hours"
RU[105]="Статистика трафика:\n  1. По годам \n  2. По месяцам \n  3. По дням \n  4. По часам"

EU[107]="1. Clear DNS query statistics"
RU[107]="1. Очистить статистику DNS-запросов"
EU[108]="2. Reset inbound traffic statistics"
RU[108]="2. Сбросить статистику трафика инбаундов"
EU[109]="3. Reset client traffic statistics"
RU[109]="3. Сбросить статистику трафика клиентов"
EU[110]="4. Reset network traffic statistics."
RU[110]="4. Сбросить статистику трафика network"

EU[111]="Client traffic statistics cleared"
RU[111]="Статистика очищена"
EU[112]="Error clearing client traffic statistics"
RU[112]="Ошибка при очистке статистики"

EU[117]="1. Add server chain for routing"
RU[117]="1. Добавить цепочку серверов для маршрутизации"
EU[118]="2. Remove server chain from configuration"
RU[118]="2. Удалить цепочку серверов из конфигурации"
EU[119]="Error adding server chain. Configuration update skipped."
RU[119]="Ошибка при добавлении цепочки серверов. Обновление конфигурации пропущено."

EU[120]="1. Show server statistics"
RU[120]="1. Показать статистику сервера"
EU[121]="2. View client DNS queries"
RU[121]="2. Просмотреть DNS-запросы клиентов"
EU[122]="3. Reset Xray server statistics"
RU[122]="3. Сбросить статистику Xray сервера"
EU[123]="4. Add new client"
RU[123]="4. Добавить нового клиента"
EU[124]="5. Delete client"
RU[124]="5. Удалить клиента"
EU[125]="6. Enable or disable client"
RU[125]="6. Включить или отключить клиента"
EU[126]="7. Set client IP address limit"
RU[126]="7. Установить лимит IP-адресов для клиента"
EU[127]="8. Update subscription auto-renewal status"
RU[127]="8. Обновить статус автопродления подписки"
EU[128]="9. Change subscription end date"
RU[128]="9. Изменить дату окончания подписки"
EU[129]=""
RU[129]="1. Полная статистика сервера"
EU[130]=""
RU[130]="2. Агреггированная статистика сервера"
EU[131]="Enter 0 to exit (updates every 10 seconds): "
RU[131]="Введите 0 для выхода (обновление каждые 10 секунд): "

###################################
### HELP MESSAGE DISPLAY
###################################
display_help_message() {
  echo
  echo "Usage: xcore [-u|--utils <true|false>] [-a|--addu <true|false>]"
  echo "         [-r|--autoupd <true|false>] [-b|--bbr <true|false>] [-i|--ipv6 <true|false>] [-w|--warp <true|false>]"
  echo "         [-c|--cert <true|false>] [-m|--mon <true|false>] [-l|--shell <true|false>] [-n|--nginx <true|false>]"
  echo "         [-p|--xray <true|false>] [--custom <true|false>] [-f|--firewall <true|false>] [-s|--ssh <true|false>]"
  echo "         [-g|--generate <true|false>] [--update] [-h|--help]"
  echo
  echo "  -u, --utils <true|false>       Additional utilities                             (default: ${defaults[utils]})"
  echo "                                 Дополнительные утилиты"
  echo "  -a, --addu <true|false>        User addition                                    (default: ${defaults[addu]})"
  echo "                                 Добавление пользователя"
  echo "  -r, --autoupd <true|false>     Automatic updates                                (default: ${defaults[autoupd]})"
  echo "                                 Автоматические обновления"
  echo "  -b, --bbr <true|false>         BBR (TCP Congestion Control)                     (default: ${defaults[bbr]})"
  echo "                                 BBR (управление перегрузкой TCP)"
  echo "  -i, --ipv6 <true|false>        Disable IPv6 support                             (default: ${defaults[ipv6]})"
  echo "                                 Отключить поддержку IPv6 "
  echo "  -w, --warp <true|false>        WARP setting                                     (default: ${defaults[warp]})"
  echo "                                 Настройка WARP"
  echo "  -c, --cert <true|false>        Certificate issuance for domain                  (default: ${defaults[cert]})"
  echo "                                 Выпуск сертификатов для домена"
  echo "  -m, --mon <true|false>         Monitoring services (node_exporter)              (default: ${defaults[mon]})"
  echo "                                 Сервисы мониторинга (node_exporter)"
  echo "  -l, --shell <true|false>       Shell In A Box installation                      (default: ${defaults[shell]})"
  echo "                                 Установка Shell In A Box"
  echo "  -n, --nginx <true|false>       NGINX installation                               (default: ${defaults[nginx]})"
  echo "                                 Установка NGINX"
  echo "  -p, --xcore <true|false>       Installing the Xray kernel                       (default: ${defaults[xray]})"
  echo "                                 Установка ядра Xray"
  echo "      --custom <true|false>      Custom JSON subscription                         (default: ${defaults[custom]})"
  echo "                                 Кастомная JSON-подписка"  
  echo "  -f, --firewall <true|false>    Firewall configuration                           (default: ${defaults[firewall]})"
  echo "                                 Настройка файрвола"
  echo "  -s, --ssh <true|false>         SSH access                                       (default: ${defaults[ssh]})"
  echo "                                 SSH доступ"
  echo "  -g, --generate <true|false>    Generate a random string for configuration       (default: ${defaults[generate]})"
  echo "                                 Генерация случайных путей для конфигурации"
  echo "      --update                   Update version of X Core manager (Version on github: ${VERSION_MANAGER})"
  echo "                                 Обновить версию X Core manager (Версия на github: ${VERSION_MANAGER})"
  echo "  -h, --help                     Display this help message"
  echo "                                 Показать это сообщение помощи"
  echo
  exit 0
}

###################################
### X CORE UPDATE MANAGER
###################################
update_xcore_manager() {
  info " Script update and integration."

  TOKEN=""
  REPO_VER_URL="https://raw.githubusercontent.com/cortez24rus/XCore/main/xcore.sh"
  GITHUB_VERSION=$(curl -s -H "Authorization: Bearer $TOKEN" "$REPO_VER_URL" | sed -n "s/^[[:space:]]*VERSION_MANAGER=[[:space:]]*'\([0-9\.]*\)'/\1/p")

  echo " Current version: $VERSION_MANAGER"

  if [[ -z "$GITHUB_VERSION" ]]; then
    error "Failed to fetch latest version from GitHub"
    return 1
  fi
  echo " Github version: $GITHUB_VERSION"

  if [[ "$VERSION_MANAGER" == "$GITHUB_VERSION" ]]; then
    warning "Script is up-to-date: $VERSION_MANAGER"
    echo
    return
  else
    warning "Updating script from $VERSION_MANAGER to $GITHUB_VERSION"
  fi

  REPO_URL="https://api.github.com/repos/cortez24rus/XCore/tarball/main"
  mkdir -p "${DIR_XCORE}/repo/"
  wget --header="Authorization: Bearer $TOKEN" -qO- $REPO_URL | tar xz --strip-components=1 -C "${DIR_XCORE}/repo/"

  chmod +x "${DIR_XCORE}/repo/xcore.sh"
  ln -sf "${DIR_XCORE}/repo/xcore.sh" /usr/local/bin/xcore
  
  chmod +x ${DIR_XCORE}/repo/cron_jobs/*
  bash "${DIR_XCORE}/repo/cron_jobs/get_v2ray-stat.sh"

  systemctl daemon-reload
  sleep 1
  systemctl restart v2ray-stat

  # Комментарий: Закомментированная строка для crontab сохранена как есть для возможного будущего использования
  # crontab -l | grep -v -- "--update" | crontab -
  # schedule_cron_job "15 5 * * * ${DIR_XCORE}/repo/xcore.sh --update"

  tilda "\n|-----------------------------------------------------------------------------|\n"
}

###################################
### LOAD DEFAULTS FROM CONFIG FILE
###################################
load_defaults_from_config() {
  if [[ -f "${DIR_XCORE}/default.conf" ]]; then
    # Чтение и выполнение строк из файла
    while IFS= read -r line; do
      # Пропускаем пустые строки и комментарии
      [[ -z "$line" || "$line" =~ ^# ]] && continue
      eval "$line"
    done < "${DIR_XCORE}/default.conf"
  else
    # Если файл не найден, используем значения по умолчанию
    defaults[utils]=true
    defaults[addu]=true
    defaults[autoupd]=true
    defaults[bbr]=true
    defaults[ipv6]=true
    defaults[warp]=false
    defaults[cert]=true
    defaults[mon]=true
    defaults[shell]=true
    defaults[nginx]=true
    defaults[xray]=true
    defaults[custom]=true
    defaults[firewall]=true
    defaults[ssh]=true
    defaults[generate]=true
  fi
}

###################################
### SAVE DEFAULTS TO CONFIG FILE
###################################
save_defaults_to_config() {
  cat > "${DIR_XCORE}/default.conf"<<EOF
defaults[utils]=false
defaults[addu]=false
defaults[autoupd]=false
defaults[bbr]=false
defaults[ipv6]=false
defaults[warp]=false
defaults[cert]=false
defaults[mon]=false
defaults[shell]=false
defaults[nginx]=true
defaults[xray]=true
defaults[custom]=true
defaults[firewall]=false
defaults[ssh]=false
defaults[generate]=true
EOF
}

###################################
### NORMALIZE CASE FOR ARGUMENTS
###################################
normalize_argument_case() {
  local key=$1
  args[$key]="${args[$key],,}"
}

###################################
### VALIDATE BOOLEAN VALUES
###################################
validate_boolean_value() {
  local key=$1
  local value=$2
  case ${value} in
    true)
      args[$key]=true
      ;;
    false)
      args[$key]=false
      ;;
    *)
      warning " $(text 75) "
      return 1
      ;;
  esac
}

###################################
### PARSE COMMAND-LINE ARGUMENTS
###################################
declare -A arg_map=(
  [-u]=utils      [--utils]=utils
  [-a]=addu       [--addu]=addu
  [-r]=autoupd    [--autoupd]=autoupd
  [-b]=bbr        [--bbr]=bbr
  [-i]=ipv6       [--ipv6]=ipv6
  [-w]=warp       [--warp]=warp
  [-c]=cert       [--cert]=cert
  [-m]=mon        [--mon]=mon
  [-l]=shell      [--shell]=shell
  [-n]=nginx      [--nginx]=nginx
  [-x]=xray       [--xray]=xray
                  [--custom]=custom
  [-f]=firewall   [--firewall]=firewall
  [-s]=ssh        [--ssh]=ssh
  [-g]=generate   [--generate]=generate
)

parse_command_line_args() {
  local opts
  opts=$(getopt -o hu:a:r:b:i:w:c:m:l:n:x:f:s:g --long utils:,addu:,autoupd:,bbr:,ipv6:,warp:,cert:,mon:,shell:,nginx:,xray:,custom:,firewall:,ssh:,generate:,update,help -- "$@")

  if [[ $? -ne 0 ]]; then
    return 1
  fi

  eval set -- "$opts"
  while true; do
    case $1 in
      --update)
        echo
        update_xcore_manager
        exit 0
        ;;
      -h|--help)
        return 1
        ;;
      --)
        shift
        break
        ;;
      *)
        if [[ -n "${arg_map[$1]}" ]]; then
          local key="${arg_map[$1]}"
          args[$key]="$2"
          normalize_argument_case "$key"
          validate_boolean_value "$key" "$2" || return 1
          shift 2
          continue
        fi
        warning " $(text 76) "
        return 1
        ;;
    esac
  done

  for key in "${!defaults[@]}"; do
    if [[ -z "${args[$key]}" ]]; then
      args[$key]=${defaults[$key]}
    fi
  done
}

###################################
### LANGUAGE SELECTION
###################################
configure_language() {
  CONF_FILE="${DIR_XCORE}/xcore.conf"

  hint " $(text 0) \n" 
  reading " $(text 1) " LANGUAGE_CHOISE

  case "$LANGUAGE_CHOISE" in
    1) NEW_LANGUAGE=EU ;;
    2) NEW_LANGUAGE=RU ;;
    *) NEW_LANGUAGE=$LANGUAGE ;; # Оставляем текущий язык, если выбор некорректен
  esac

  sed -i "s/^LANGUAGE=.*/LANGUAGE=$NEW_LANGUAGE/" "$CONF_FILE"

  source "$CONF_FILE"
}

###################################
### OPERATING SYSTEM DETECTION
###################################
detect_operating_system() {
  if [ -s /etc/os-release ]; then
    SYS="$(grep -i pretty_name /etc/os-release | cut -d \" -f2)"
  elif [ -x "$(type -p hostnamectl)" ]; then
    SYS="$(hostnamectl | grep -i system | cut -d : -f2)"
  elif [ -x "$(type -p lsb_release)" ]; then
    SYS="$(lsb_release -sd)"
  elif [ -s /etc/lsb-release ]; then
    SYS="$(grep -i description /etc/lsb-release | cut -d \" -f2)"
  elif [ -s /etc/redhat-release ]; then
    SYS="$(grep . /etc/redhat-release)"
  elif [ -s /etc/issue ]; then
    SYS="$(grep . /etc/issue | cut -d '\' -f1 | sed '/^[ ]*$/d')"
  fi

  REGEX=("debian" "ubuntu" "centos|red hat|kernel|alma|rocky")
  RELEASE=("Debian" "Ubuntu" "CentOS")
  EXCLUDE=("---")
  MAJOR=("10" "20" "7")
  PACKAGE_UPDATE=("apt -y update" "apt -y update" "yum -y update --skip-broken")
  PACKAGE_INSTALL=("apt -y install" "apt -y install" "yum -y install")
  PACKAGE_UNINSTALL=("apt -y autoremove" "apt -y autoremove" "yum -y autoremove")

  for int in "${!REGEX[@]}"; do
    [[ "${SYS,,}" =~ ${REGEX[int]} ]] && SYSTEM="${RELEASE[int]}" && break
  done

  # Проверка на кастомизированные системы от различных производителей
  if [ -z "$SYSTEM" ]; then
    [ -x "$(type -p yum)" ] && int=2 && SYSTEM='CentOS' || error " $(text 5) "
  fi

  # Определение основной версии Linux
  MAJOR_VERSION=$(sed "s/[^0-9.]//g" <<< "$SYS" | cut -d. -f1)

  # Сначала исключаем системы, указанные в EXCLUDE, затем для оставшихся делаем сравнение по основной версии
  for ex in "${EXCLUDE[@]}"; do [[ ! "${SYS,,}" =~ $ex ]]; done &&
  [[ "$MAJOR_VERSION" -lt "${MAJOR[int]}" ]] && error " $(text 71) "
}

###################################
### DEPENDENCY CHECK AND INSTALLATION
###################################
install_dependencies() {
  # Зависимости, необходимые для трех основных систем
  [ "${SYSTEM}" = 'CentOS' ] && ${PACKAGE_INSTALL[int]} vim-common epel-release
  DEPS_CHECK=("ping" "wget" "curl" "systemctl" "ip" "sudo")
  DEPS_INSTALL=("iputils-ping" "wget" "curl" "systemctl" "iproute2" "sudo")

  for g in "${!DEPS_CHECK[@]}"; do
    [ ! -x "$(type -p ${DEPS_CHECK[g]})" ] && [[ ! "${DEPS[@]}" =~ "${DEPS_INSTALL[g]}" ]] && DEPS+=(${DEPS_INSTALL[g]})
  done

  if [ "${#DEPS[@]}" -ge 1 ]; then
    info "\n $(text 72) ${DEPS[@]} \n"
    ${PACKAGE_UPDATE[int]}
    ${PACKAGE_INSTALL[int]} ${DEPS[@]}
  else
    info "\n $(text 73) \n"
  fi
}

###################################
### ROOT PRIVILEGE CHECK
###################################
verify_root_privileges() {
  if [[ $EUID -ne 0 ]]; then
    error " $(text 2) "
  fi
}

###################################
### EXTERNAL IP ADDRESS DETECTION
###################################
detect_external_ip() {
  IP4=$(curl -s https://cloudflare.com/cdn-cgi/trace | grep "ip" | cut -d "=" -f 2)

  if [[ ! $IP4 =~ ${regex[ipv4]} ]]; then
    IP4=$(curl -s ipinfo.io/ip)
  fi

  if [[ ! $IP4 =~ ${regex[ipv4]} ]]; then
    IP4=$(curl -s 2ip.io)
  fi

  if [[ ! $IP4 =~ ${regex[ipv4]} ]]; then
    error " $(text 3) " # Комментарий: Добавлен выход с ошибкой, если IP не удалось определить
  fi
}

###################################
### BANNER DISPLAY
###################################
display_xcore_banner() {
  echo
  echo " █░█ ░░ █▀▀█ █▀▀█ █▀▀█ █▀▀ "
  echo " ▄▀▄    █░░  █░░█ █▄▄▀ █▀▀ "
  echo " ▀░▀ ░░ ▀▀▀▀ ▀▀▀▀ ▀░▀▀ ▀▀▀ $VERSION_MANAGER"
  echo
}

###################################
### GENERATE UUID FOR XRAY CONFIGURATION
###################################
generate_uuid() {
  local XRAY_UUID=$(cat /proc/sys/kernel/random/uuid)
  echo "$XRAY_UUID"
}

###################################
### EXTRACT DATA FROM HAPROXY CONFIG
###################################
extract_data() {
  SUB_JSON_PATH=""
  for dir in /var/www/*/ ; do
      dir_name=$(basename "$dir")
      [ ${#dir_name} -eq 30 ] && SUB_JSON_PATH="$dir_name" && break
  done
  if [[ -z "$SUB_JSON_PATH" ]]; then
    error "Ошибка: директория с длиной имени 30 символов не найдена в /var/www/"
  fi

  local CONFIG_FILE_HAPROXY="${DIR_HAPROXY}/haproxy.cfg"
  detect_external_ip
  CURR_DOMAIN=$(grep -oP 'crt /etc/haproxy/certs/\K[^.]+(?:\.[^.]+)+(?=\.pem)' "$CONFIG_FILE_HAPROXY")
  if [[ -z "$CURR_DOMAIN" ]]; then
    error "Ошибка: не удалось извлечь домен из haproxy.cfg"
  fi

#  echo $SUB_JSON_PATH
#  echo $CURR_DOMAIN
#  echo $CONFIG_FILE_HAPROXY
#  echo $IP4
}

###################################
### ADD USER TO XRAY CONFIGURATION
###################################
add_user_to_xray() {
  curl -s -X POST http://127.0.0.1:9952/api/v1/add_user -d "user=${USERNAME}&credential=${XRAY_UUID}&inboundTag=vless-in"
}

###################################
### CONFIGURE XRAY CLIENT SETTINGS
###################################
configure_xray_client() {
  # Устанавливаем TEMPLATE_FILE в зависимости от значения CHAIN
  if [ "$CHAIN" = "false" ]; then
    TEMPLATE_FILE="${DIR_XCORE}/repo/conf_template/client-vless-raw.json"
  else
    TEMPLATE_FILE="${DIR_XCORE}/repo/conf_template/client-vless-raw-chain.json"
  fi

  cp -r "$TEMPLATE_FILE" "/var/www/${SUB_JSON_PATH}/vless_raw/${USERNAME}.json"

  sed -i \
    -e "s/IP_TEMP/${IP4}/g" \
    -e "s/DOMAIN_TEMP/${DOMAIN}/g" \
    -e "s/UUID_TEMP/${XRAY_UUID}/g" \
    "/var/www/${SUB_JSON_PATH}/vless_raw/${USERNAME}.json"
}

###################################
### ADD NEW USER CONFIGURATION
###################################
add_new_user() {
  while true; do
    echo -n "Введите имя пользователя (или '0' для возврата в меню): "
    read USERNAME

    case "$USERNAME" in
      0)
        echo "Возврат в меню..."
        return  # Возврат в меню, завершая функцию
        ;;
      "")
        echo "Имя пользователя не может быть пустым. Попробуйте снова."
        ;;
      *)
        if jq -e ".inbounds[] | select(.tag == \"vless-in\") | .settings.clients[] | select(.email == \"$USERNAME\")" "${DIR_XRAY}/config.json" > /dev/null; then
          echo "Пользователь $USERNAME уже добавлен в Xray. Попробуйте другое имя."
          echo
          continue
        fi

        if [[ -f /var/www/${SUB_JSON_PATH}/vless_raw/${USERNAME}.json ]]; then
          echo "Файл конфигурации для $USERNAME уже существует. Удалите его или выберите другое имя."
          echo
          continue
        fi

        read XRAY_UUID < <(generate_uuid)

        add_user_to_xray
        if [[ $? -ne 0 ]]; then
          echo "Не удалось добавить пользователя через API. Пробуем обновить config.json напрямую..."
          inboundnum=$(jq '[.inbounds[].tag] | index("vless-in")' ${DIR_XRAY}/config.json)
          jq ".inbounds[${inboundnum}].settings.clients += [{\"email\":\"${USERNAME}\",\"id\":\"${XRAY_UUID}\"}]" "${DIR_XRAY}/config.json" > "${DIR_XRAY}/config.json.tmp" && mv "${DIR_XRAY}/config.json.tmp" "${DIR_XRAY}/config.json"

          sed -i "/local users = {/,/}/ s/}/  [\"${USERNAME}\"] = \"${XRAY_UUID}\",\n}/" "${DIR_HAPROXY}/.auth.lua"
        fi
        DOMAIN=$CURR_DOMAIN
        configure_xray_client

        systemctl reload haproxy && systemctl restart xray

        echo "Пользователь $USERNAME добавлен с UUID: $XRAY_UUID"
        echo
        ;;
    esac
  done
}

###################################
### DELETE USER SUBSCRIPTION CONFIG
###################################
delete_subscription_config() {
  if [[ -f /var/www/${SUB_JSON_PATH}/vless_raw/${USERNAME}.json ]]; then
    rm -rf /var/www/${SUB_JSON_PATH}/vless_raw/${USERNAME}.json
  fi
}

##################################
### DELETE USER FROM XRAY SERVER CONFIG
###################################
delete_from_xray_server() {
  curl -X DELETE "http://127.0.0.1:9952/api/v1/delete_user?user=${USERNAME}&inboundTag=vless-in"
}

###################################
### EXTRACT USERS FROM XRAY CONFIG
###################################
extract_xray_users() {
  jq -r '.inbounds[] | select(.tag == "vless-in") | .settings.clients[] | "\(.email) \(.id)"' "${DIR_XRAY}/config.json"
}

###################################
### DELETE USER CONFIGURATION
###################################
delete_user() {
  while true; do
    mapfile -t clients < <(extract_xray_users)
    if [ ${#clients[@]} -eq 0 ]; then
      echo "Нет пользователей для отображения."
      return
    fi

    info " Список пользователей:"
    local count=1
    declare -A user_map

    for client in "${clients[@]}"; do
      IFS=' ' read -r email id <<< "$client"
      echo "$count. $email (ID: $id)"
      user_map[$count]="$email $id"
      ((count++))
    done
    echo "0. Выйти"

    # Запрос на выбор пользователей
    read -p "Введите номера пользователей через запятую: " choices
    echo

    # Разбиение введенных номеров на массив
    IFS=', ' read -r -a selected_users <<< "$choices"
    for choice in "${selected_users[@]}"; do
      case "$choice" in
        0)
          echo "Выход..."
          return
          ;;
        ''|*[!0-9]*)
          echo "Ошибка: введите корректный номер."
          ;;
        *)
          if [[ -n "${user_map[$choice]}" ]]; then
            IFS=' ' read -r USERNAME XRAY_UUID <<< "${user_map[$choice]}"
            echo "Вы выбрали: $USERNAME (ID: $XRAY_UUID)"
            
            delete_from_xray_server
            if [[ $? -ne 0 ]]; then
              echo "Не удалось удалить пользователя через API. Пробуем обновить config.json напрямую..."
              inboundnum=$(jq '[.inbounds[].tag] | index("vless-in")' ${DIR_XRAY}/config.json)
              jq "del(.inbounds[${inboundnum}].settings.clients[] | select(.email==\"${USERNAME}\"))" "${DIR_XRAY}/config.json" > "${DIR_XRAY}/config.json.tmp" && mv "${DIR_XRAY}/config.json.tmp" "${DIR_XRAY}/config.json"

              sed -i "/\[\"${USERNAME//\"/\\\"}\"\] = \".*\",/d" "${DIR_HAPROXY}/.auth.lua"
            fi
            delete_subscription_config
          else
            echo "Некорректный номер: $choice"
          fi
          ;;
      esac
    done
  systemctl reload nginx && systemctl reload haproxy && systemctl restart xray
  echo
  echo "|--------------------------------------------------------------------------|"
  echo
  done
}

###################################
### DISPLAY USER LIST FROM API
###################################
display_user_list1() {
  local API_URL="http://127.0.0.1:9952/api/v1/users"
  local field="$1"  # Поле для извлечения, например "enabled", "lim_ip", "renew", "sub_end"

  declare -gA user_map
  local counter=0

  # Получаем данные от API
  response=$(curl -s -X GET "$API_URL")
  if [ $? -ne 0 ]; then
    warning "Ошибка: Не удалось подключиться к API"
    return 1
  fi

  # Парсим JSON, извлекая email и указанное поле
  mapfile -t users < <(echo "$response" | jq -r --arg field "$field" '.[] | [.user, .[$field]] | join("|")')

  if [ ${#users[@]} -eq 0 ]; then
    info "Нет пользователей для отображения"
    return 1
  fi

  info " Список пользователей:"
  for user in "${users[@]}"; do
    IFS='|' read -r email value <<< "$user"
    user_map[$counter]="$email"
    echo " $((counter+1)). $email ($field: ${value:-не задано})"
    ((counter++))
  done

  # Сохраняем user_map и users для использования в вызывающей функции
  export user_map
  export users
  return 0
}

###################################
### UPDATE USER PARAMETER VIA API
###################################
update_user_parameter_patch() {
  local param_name="$1"  # Название параметра, например "lim_ip", "renew", "offset", "count"
  local api_url="$2"     # URL для GET-запроса
  local prompt="$3"      # Текст для запроса нового значения

  last_selected_num=""
  local param_value

  # Запрос нового значения
  read -p "$prompt: " param_value
  clear

  while true; do
    # Получаем и отображаем список пользователей
    display_user_list1 "$param_name"
    if [ $? -ne 0 ]; then
      return 1
    fi

    info " (Выбрано значение $param_name: $param_value)"
    # Если последний выбранный номер существует, предлагаем его по умолчанию
    if [ -n "$last_selected_num" ]; then
      read -p " Введите номера пользователей (0 - выход, 'reset' - изменить $param_name): " choice
    else
      read -p " Введите номера пользователей (0 - выход, 'reset' - изменить $param_name): " choice
    fi

    # Если нажат Enter и есть последний выбор, используем его
    if [ -z "$choice" ] && [ -n "$last_selected_num" ]; then
      choice="$last_selected_num"
    fi

    if [[ "$choice" == "0" ]]; then
      info "Выход..."
      return
    fi

    if [[ "$choice" == "reset" ]]; then
      clear
      read -p "$prompt: " param_value
      continue
    fi

    # Разбиваем ввод на массив номеров
    choices=($(echo "$choice" | tr ',' ' ' | tr -s ' ' | tr ' ' '\n'))

    # Проверяем каждый номер
    for num in "${choices[@]}"; do
      if [[ ! "$num" =~ ^[0-9]+$ ]] || (( num < 1 || num > ${#users[@]} )); then
        warning "Некорректный номер пользователя: $num. Попробуйте снова."
        continue 2
      fi
    done

    clear
    # Обновляем параметр для выбранных пользователей и запоминаем последний номер
    for num in "${choices[@]}"; do
      selected_email="${user_map[$((num-1))]}"
      curl -s -X PATCH "${api_url}?user=${selected_email}&$param_name=${param_value}"
      # Запоминаем последний выбранный номер
      last_selected_num="$num"
    done
  done
}

###################################
### TOGGLE USER STATUS VIA API
###################################
toggle_user_status() {
  update_user_parameter_patch "enabled" "http://127.0.0.1:9952/api/v1/set_enabled" "Введите true для включения и false отключения клиента"
}

###################################
### SET IP LIMIT FOR USER
###################################
set_user_lim_ip() {
  update_user_parameter_patch "lim_ip" "http://127.0.0.1:9952/api/v1/update_lim_ip" "Введите лимит IP"
}

###################################
### UPDATE USER RENEWAL STATUS
###################################
update_user_renewal() {
  update_user_parameter_patch "renew" "http://127.0.0.1:9952/api/v1/update_renew" "Введите значение для продления подписки"
}

###################################
### ADJUST USER SUBSCRIPTION END DATE
###################################
adjust_subscription_date() {
  update_user_parameter_patch "sub_end" "http://127.0.0.1:9952/api/v1/adjust_date" "Введите значение sub_end (например, +1d, -1d3h, 0)"
}

###################################
### RESET STATISTICS SUBMENU
###################################
reset_stats_menu() {
  while true; do
    clear
    display_xcore_banner
    tilda "|--------------------------------------------------------------------------|"
    info " $(text 107) "    # 1. Clear DNS query statistics
    info " $(text 108) "    # 2. Reset inbound traffic statistics
    info " $(text 109) "    # 3. Reset client traffic statistics
    info " $(text 110) "    # 4. Сброс трафика network
    echo
    warning " $(text 84) "  # 0. Previous menu
    tilda "|--------------------------------------------------------------------------|"
    echo
    reading " $(text 1) " CHOICE_MENU
    case $CHOICE_MENU in
      1)
        curl -s -X POST http://127.0.0.1:9952/api/v1/delete_dns_stats && info " $(text 111) " || warning " $(text 112) "
        sleep 2
        ;;
      2)
        curl -s -X POST http://127.0.0.1:9952/api/v1/reset_traffic_stats && info " $(text 111) " || warning " $(text 112) "
        sleep 2
        ;;
      3)
        curl -s -X POST http://127.0.0.1:9952/api/v1/reset_clients_stats && info " $(text 111) " || warning " $(text 112) "
        sleep 2
        ;;
      4)
        curl -s -X POST http://127.0.0.1:9952/api/v1/reset_traffic && info " $(text 111) " || warning " $(text 112) "
        sleep 2
        ;;
      0) break ;;
      *) warning " $(text 76) " ;;
    esac
  done
}




##################################
### DISPLAY NODE LIST FROM API
###################################
display_node_list() {
  local API_URL="http://127.0.0.1:9952/api/v1/users"
  declare -gA node_map
  local counter=0

  # Получаем данные от API
  response=$(curl -s -X GET "$API_URL")
  if [ $? -ne 0 ]; then
    warning "Ошибка: Не удалось подключиться к API"
    return 1
  fi

  # Извлекаем уникальные имена нод
  mapfile -t nodes < <(echo "$response" | jq -r '.[].node' | sort -u)

  if [ ${#nodes[@]} -eq 0 ]; then
    info "Нет нод для отображения"
    return 1
  fi

  info " Список нод:"
  echo
  echo " 1. Все ноды"
  node_map[1]="all"
  local counter=2
  for node in "${nodes[@]}"; do
    echo " $counter. $node"
    node_map[$counter]="$node"
    ((counter++))
  done
  echo
  warning " $(text 84) " # 0. Previous menu

  export node_map
  export nodes
  return 0
}

###################################
### DISPLAY USER LIST FROM API
###################################
display_user_list() {
  local API_URL="http://127.0.0.1:9952/api/v1/users"
  local selected_nodes="$1"  # Ожидаем строку нод, разделённых запятыми, или "all"
  declare -gA user_map
  local counter=0

  # Получаем данные от API
  response=$(curl -s -X GET "$API_URL")
  if [ $? -ne 0 ]; then
    warning "Ошибка: Не удалось подключиться к API"
    return 1
  fi

  # Извлекаем уникальных пользователей
  if [ "$selected_nodes" == "all" ]; then
    mapfile -t users < <(echo "$response" | jq -r '.[].users[].user' | sort -u)
  else
    # Формируем фильтр для jq с использованием IN
    nodes=$(echo "$selected_nodes" | sed 's/,/","/g; s/^/"/; s/$/"/')
    mapfile -t users < <(echo "$response" | jq -r --argjson nodes "[${nodes}]" '.[] | select(.node | IN($nodes[])) | .users[].user' | sort -u)
  fi

  if [ ${#users[@]} -eq 0 ]; then
    info "Нет пользователей для отображения"
    return 1
  fi

  info " Список пользователей:"
  echo
  echo " 1. Все пользователи"
  user_map[1]="all"
  local counter=2
  for user in "${users[@]}"; do
    echo " $counter. $user"
    user_map[$counter]="$user"
    ((counter++))
  done
  echo
  warning " $(text 84) " # 0. Previous menu

  export user_map
  export users
  return 0
}

###################################
### FETCH DNS STATISTICS
###################################
fetch_dns_stats() {
  local API_URL="http://127.0.0.1:9952/api/v1/dns_stats"
  local selected_node="all"
  local selected_user="all"
  local count=""

  # Выбор ноды
  while true; do
    clear
    display_xcore_banner
    tilda "|--------------------------------------------------------------------------|"
    display_node_list
    if [ $? -ne 0 ]; then
      info " $(text 85) "
      read -r
      return
    fi
    tilda "|--------------------------------------------------------------------------|"
    reading " $(text 1) " node_choice
    if [ -z "$node_choice" ]; then
      node_choice="1"  # По умолчанию "Со всех нод"
    fi
    case "$node_choice" in
      0) return ;;
      [1-9]*)
        if [ -n "${node_map[$node_choice]}" ]; then
          selected_node="${node_map[$node_choice]}"
          break
        else
          warning " $(text 33) "
          sleep 2
        fi
        ;;
      *) warning " $(text 33) "; sleep 2 ;;
    esac
  done

  # Выбор пользователя
  while true; do
    clear
    display_xcore_banner
    tilda "|--------------------------------------------------------------------------|"
    display_user_list "$selected_node"
    if [ $? -ne 0 ]; then
      info " $(text 85) "
      read -r
      return
    fi
    tilda "|--------------------------------------------------------------------------|"
    reading " $(text 1) " user_choice
    if [ -z "$user_choice" ]; then
      user_choice="1"  # По умолчанию "Все пользователи"
    fi
    case "$user_choice" in
      0) return ;;
      [1-9]*)
        if [ -n "${user_map[$user_choice]}" ]; then
          selected_user="${user_map[$user_choice]}"
          break
        else
          warning " $(text 33) "
          sleep 2
        fi
        ;;
      *) warning " $(text 33) "; sleep 2 ;;
    esac
  done

  # Выбор количества строк (count)
  while true; do
    clear
    display_xcore_banner
    tilda "|--------------------------------------------------------------------------|"
    info " Строки вывода на экран"
    echo
    echo " Введите количество строк (по умолчанию 20)"
    echo
    warning " $(text 84) " # 0. Previous menu
    tilda "|--------------------------------------------------------------------------|"
    reading " $(text 1) " count_choice
    if [ -z "$count_choice" ]; then
      count=""  # Пустое значение для использования значения по умолчанию (20)
      break
    fi
    case "$count_choice" in
      0) return ;;
      [1-9]*)
        if [[ "$count_choice" =~ ^[0-9]+$ ]]; then
          count="$count_choice"
          break
        else
          warning "Ошибка: введите число."
          sleep 2
        fi
        ;;
      *) warning " $(text 33) "; sleep 2 ;;
    esac
  done

  # Выполнение запроса с обновлением каждые 20 секунд
  local domain=""
  while true; do
    clear
    local query=""
    [ "$selected_node" != "all" ] && query="node=$selected_node"
    [ "$selected_user" != "all" ] && query="$query${query:+&}user=$selected_user"
    [ -n "$count" ] && query="$query${query:+&}count=$count"
    [ -n "$domain" ] && query="$query${query:+&}domain=$domain"
    
    info " Выбрана нода: $selected_node; Выбран пользователь: $selected_user; Количество строк: ${count:-20}; Домен: ${domain:-не указан}"
    curl -s -X GET "$API_URL${query:+?$query}"
    echo -n "Введите значение для фильтрации по домену (например: com, google) или 0 для выхода: "
    read -t 20 -r sub_choice
    case "$sub_choice" in
      0) break ;;
      "") ;; # Пропуск, если ничего не введено, сохраняем текущий домен
      *) domain="$sub_choice" ;; # Обновляем домен новым значением
    esac
  done
}

###################################
### FETCH TRAFFIC STATISTICS
###################################
fetch_traffic_stats() {
  local API_URL="http://127.0.0.1:9952/api/v1/stats"
  local query=""
  local selected_nodes=""
  local selected_users=""
  local sort_by=""
  local sort_order=""
  local aggregate=""

  # Выбор сортировки
  while true; do
    clear
    display_xcore_banner
    tilda "|--------------------------------------------------------------------------|"
    info " Доступные колонки для сортировки:"
    echo
    echo " 1. node_name (Имя ноды)"
    echo " 2. user (Пользователь)"
    echo " 3. last_seen (Последний визит)"
    echo " 4. rate (Скорость)"
    echo " 5. uplink (Входящий трафик)"
    echo " 6. downlink (Исходящий трафик)"
    echo " 7. sess_uplink (Сессионный входящий)"
    echo " 8. sess_downlink (Сессионный исходящий)"
    echo " 9. created (Дата создания)"
    echo
    warning " $(text 84) " # 0. Previous menu
    tilda "|--------------------------------------------------------------------------|"
    reading " $(text 1) " sort_choice
    if [ -z "$sort_choice" ]; then
      sort_by=""
      break
    fi
    case $sort_choice in
      1) sort_by="node_name"; break ;;
      2) sort_by="user"; break ;;
      3) sort_by="last_seen"; break ;;
      4) sort_by="rate"; break ;;
      5) sort_by="uplink"; break ;;
      6) sort_by="downlink"; break ;;
      7) sort_by="sess_uplink"; break ;;
      8) sort_by="sess_downlink"; break ;;
      9) sort_by="created"; break ;;
      0) return ;;
      *) warning " $(text 33) "; sleep 2 ;;
    esac
  done

  # Выбор порядка сортировки
  if [ -n "$sort_by" ]; then
    while true; do
      clear
      display_xcore_banner
      tilda "|--------------------------------------------------------------------------|"
      info " Выберите порядок сортировки:"
      echo
      echo " 1. ASC (по возрастанию)"
      echo " 2. DESC (по убыванию)"
      echo
      warning " $(text 84) " # 0. Previous menu
      tilda "|--------------------------------------------------------------------------|"
      reading " $(text 1) " order_choice
      if [ -z "$order_choice" ]; then
        sort_order=""
        break
      fi
      case $order_choice in
        1) sort_order="ASC"; break ;;
        2) sort_order="DESC"; break ;;
        0) return ;;
        *) warning " $(text 33) "; sleep 2 ;;
      esac
    done
  fi

  # Выбор агрегации
  while true; do
    clear
    display_xcore_banner
    tilda "|--------------------------------------------------------------------------|"
    info " Агрегировать статистику?"
    echo
    echo " 1. Да (aggregate=true)"
    echo " 2. Нет (aggregate=false)"
    echo
    warning " $(text 84) " # 0. Previous menu
    tilda "|--------------------------------------------------------------------------|"
    reading " $(text 1) " aggregate_choice
    if [ -z "$aggregate_choice" ]; then
      aggregate=""
      break
    fi
    case $aggregate_choice in
      1) aggregate="true"; break ;;
      2) aggregate="false"; break ;;
      0) return ;;
      *) warning " $(text 33) "; sleep 2 ;;
    esac
  done

  # Выбор нод
  while true; do
    clear
    display_xcore_banner
    tilda "|--------------------------------------------------------------------------|"
    display_node_list
    if [ $? -ne 0 ]; then
      info " $(text 85) "
      read -r
      return
    fi
    tilda "|--------------------------------------------------------------------------|"
    reading " Введите номера нод через запятую (или Enter для всех): " node_choice
    if [ -z "$node_choice" ]; then
      selected_nodes="all"
      break
    fi
    IFS=',' read -r -a node_array <<< "$node_choice"
    valid=true
    selected_nodes=""
    for node_num in "${node_array[@]}"; do
      node_num=$(echo "$node_num" | xargs) # Удаляем пробелы
      if [ "$node_num" == "1" ]; then
        selected_nodes="all"
        break
      elif [ -n "${node_map[$node_num]}" ]; then
        selected_nodes="${selected_nodes},${node_map[$node_num]}"
      else
        valid=false
        warning " Неверный номер ноды: $node_num"
      fi
    done
    if [ "$valid" = true ]; then
      selected_nodes=${selected_nodes#,} # Удаляем начальную запятую
      break
    fi
    sleep 2
  done

  # Выбор пользователей
  while true; do
    clear
    display_xcore_banner
    tilda "|--------------------------------------------------------------------------|"
    display_user_list "$selected_nodes"
    if [ $? -ne 0 ]; then
      info " $(text 85) "
      read -r
      return
    fi
    tilda "|--------------------------------------------------------------------------|"
    reading " Введите номера пользователей через запятую (или Enter для всех): " user_choice
    if [ -z "$user_choice" ]; then
      selected_users="all"
      break
    fi
    IFS=',' read -r -a user_array <<< "$user_choice"
    valid=true
    selected_users=""
    for user_num in "${user_array[@]}"; do
      user_num=$(echo "$user_num" | xargs) # Удаляем пробелы
      if [ "$user_num" == "1" ]; then
        selected_users="all"
        break
      elif [ -n "${user_map[$user_num]}" ]; then
        selected_users="${selected_users},${user_map[$user_num]}"
      else
        valid=false
        warning " Неверный номер пользователя: $user_num"
      fi
    done
    if [ "$valid" = true ]; then
      selected_users=${selected_users#,} # Удаляем начальную запятую
      break
    fi
    sleep 2
  done

  # Формируем параметры запроса
  [ -n "$sort_by" ] && query="sort_by=$sort_by"
  [ -n "$sort_order" ] && query="$query${query:+&}sort_order=$sort_order"
  [ -n "$aggregate" ] && query="$query${query:+&}aggregate=$aggregate"
  [ "$selected_nodes" != "all" ] && query="$query${query:+&}node=$selected_nodes"
  [ "$selected_users" != "all" ] && query="$query${query:+&}user=$selected_users"

  # Выполнение запроса с обновлением каждые 20 секунд
  while true; do
    clear
    info "Сортировка > ${sort_by:--} | Порядок > ${sort_order:--} | Агрегация > ${aggregate:--} | Ноды > ${selected_nodes:-все} | Пользователи > ${selected_users:-все}"
    echo
    # URL-кодирование параметров для корректной передачи
    encoded_query=$(echo "$query" | sed 's/,/%2C/g')
    curl -s -X GET "$API_URL${encoded_query:+?$encoded_query}"
    echo -n "$(text 131) " # Нажмите 0 для выхода или дождитесь обновления (20 секунд)
    read -t 20 -r sub_choice
    case "$sub_choice" in
      0) break ;;
      *) ;; # Продолжить цикл, если ничего не введено
    esac
  done
}






###################################
### XRAY CORE MANAGEMENT MENU
###################################
manage_xray_core() {
  while true; do
    clear
    extract_data
    display_xcore_banner
    tilda "|--------------------------------------------------------------------------|"
    info " $(text 120) "    # 1. Show server statistics
    info " $(text 121) "    # 2. View client DNS queries
    info " $(text 122) "    # 3. Reset Xray server statistics
    echo
    info " $(text 123) "    # 4. Add new client
    info " $(text 124) "    # 5. Delete client
    info " $(text 125) "    # 6. Enable or disable client
    echo
    info " $(text 126) "    # 7. Set client IP address limit
    info " $(text 127) "    # 8. Update subscription auto-renewal status
    info " $(text 128) "    # 9. Change subscription end date
    info " $(text 96) "     # 12. Change interface language
    echo
    warning " $(text 84) "  # 0. Previous menu
    tilda "|--------------------------------------------------------------------------|"
    echo
    reading " $(text 1) " CHOICE_MENU
    tilda "$(text 10)"
    case $CHOICE_MENU in
      1) fetch_traffic_stats ;;
      2) fetch_dns_stats ;;
      3) reset_stats_menu ;;
      4) add_new_user ;;
      5) delete_user ;;
      6) toggle_user_status ;;
      7) set_user_lim_ip ;;
      8) update_user_renewal ;;
      9) adjust_subscription_date ;;
      12) configure_language ;;
      0) break ;;
      *) warning " $(text 76) " ;;
    esac
  done
}

###################################
### FUNCTION INITIALIZE CONFIG
###################################
init_file() {
  if [ ! -f "${DIR_XCORE}/xcore.conf" ]; then
    mkdir -p ${DIR_XCORE}
    cat > "${DIR_XCORE}/xcore.conf" << EOF
LANGUAGE=EU
CHAIN=false
EOF
  fi
}

###################################
### MAIN FUNCTION
###################################
main() {
  init_file
  source "${DIR_XCORE}/xcore.conf"
  load_defaults_from_config
  parse_command_line_args "$@" || display_help_message
  verify_root_privileges
  detect_external_ip
  echo
  manage_xray_core
}

main "$@"
