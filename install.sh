#!/opt/bin/sh

REPO=klaymm/pc-pult
PREFIX=${PCR_PREFIX:-/opt}
BIN=$PREFIX/sbin/pc-pult-router
INIT=$PREFIX/etc/init.d/S99pc-pult
CFGDIR=$PREFIX/etc/pc-pult
CFG=$CFGDIR/config.json
LOGDIR=$PREFIX/var/log

tty_ok() { ( : < /dev/tty ) 2>/dev/null; }

ask_yes() {
  tty_ok || return 1
  printf '%s' "$1" > /dev/tty
  read -r answer < /dev/tty || return 1
  case "$answer" in y|Y|yes|YES|д|Д|да|Да) return 0 ;; esac
  return 1
}

die() {
  echo "$1" >&2
  exit 1
}

detect_arch() {
  archs=$(opkg print-architecture 2>/dev/null | awk '$1 == "arch" { print $2 }')
  case "$archs" in
    *aarch64*) echo arm64 ;;
    *mipsel*) echo mipsle ;;
    *mips*) echo mips ;;
    *armv7*|*armv6*|*armv5*) echo arm ;;
    *) return 1 ;;
  esac
}

fetch() {
  wget -q -T 30 -t 3 -O "$2" "$1"
}

svc() {
  [ "$PREFIX" = /opt ] && [ -x "$INIT" ] && "$INIT" "$1" >/dev/null 2>&1
}

remove_all() {
  svc stop
  rm -f "$INIT" "$BIN" "$BIN.new" "$LOGDIR/pc-pult.log" "$LOGDIR/pc-pult.log.1" "$LOGDIR/pc-pult.log.crash"
  if [ -d "$CFGDIR" ]; then
    if ask_yes "Удалить и настройки с токеном бота и списком пользователей? [y/N] "; then
      rm -rf "$CFGDIR"
      echo "Всё удалено."
    else
      echo "Программа удалена, настройки остались в $CFGDIR."
    fi
  else
    echo "Программа удалена."
  fi
}

main() {
  PATH=/opt/sbin:/opt/bin:/opt/usr/bin:/usr/sbin:/usr/bin:/sbin:/bin
  export PATH

  if [ "$1" = "remove" ]; then
    remove_all
    exit 0
  fi

  if [ ! -x /opt/bin/opkg ]; then
    die "Нужен Entware: https://help.keenetic.com/hc/ru/articles/360021888880"
  fi

  goarch=$(detect_arch) || die "Не удалось определить процессор роутера: $(opkg print-architecture 2>/dev/null | awk '$1 == "arch" { printf "%s ", $2 }')"

  if [ -n "$PCR_BASE" ]; then
    base=$PCR_BASE
  elif [ -n "$PCR_VERSION" ]; then
    base="https://github.com/$REPO/releases/download/$PCR_VERSION"
  else
    base="https://github.com/$REPO/releases/latest/download"
  fi

  if ! command -v sha256sum >/dev/null 2>&1; then
    opkg install coreutils-sha256sum >/dev/null 2>&1 || die "Не нашёл sha256sum и не смог поставить: opkg install coreutils-sha256sum"
  fi

  mkdir -p /opt/tmp
  work=$(mktemp -d /opt/tmp/pc-pult.XXXXXX) || exit 1
  trap 'rm -rf "$work"' EXIT

  echo "Скачиваю pc-pult ($goarch)"
  name="pc-pult-router-linux-$goarch"
  if ! fetch "$base/$name" "$work/$name" || ! fetch "$base/S99pc-pult" "$work/S99pc-pult" || ! fetch "$base/SHA256SUMS" "$work/SHA256SUMS"; then
    die "Не удалось скачать $base/$name. Для HTTPS нужны пакеты ca-certificates и wget-ssl, а релиз должен существовать."
  fi

  for f in "$name" S99pc-pult; do
    want=$(awk -v f="$f" '$2 == f { print $1 }' "$work/SHA256SUMS")
    have=$(sha256sum "$work/$f" | awk '{ print $1 }')
    if [ -z "$want" ] || [ "$want" != "$have" ]; then
      die "Контрольная сумма $f не совпала, установка остановлена."
    fi
  done

  chmod +x "$work/$name"
  version=$("$work/$name" -version 2>/dev/null) || die "Скачанная программа не запускается на этом роутере."

  mkdir -p "$PREFIX/sbin" "$PREFIX/etc/init.d" "$LOGDIR"
  mkdir -p "$CFGDIR"
  svc stop
  cp "$work/$name" "$BIN.new" && mv "$BIN.new" "$BIN" || die "Не удалось записать $BIN"
  cp "$work/S99pc-pult" "$INIT" && chmod +x "$INIT" || die "Не удалось записать $INIT"
  echo "Установлена версия $version"

  if [ -f "$CFG" ]; then
    svc start
    echo "Настройки сохранены, служба перезапущена. Журнал: tail -f $LOGDIR/pc-pult.log"
    exit 0
  fi

  if ! tty_ok; then
    echo "Нет терминала для вопросов. Запустите настройку вручную: $BIN -setup"
    exit 1
  fi
  if [ "$PREFIX" = /opt ]; then
    "$BIN" -setup -config "$CFG" < /dev/tty
  else
    "$BIN" -setup -config "$CFG" -port "${PCR_PORT:-18765}" -no-start < /dev/tty
  fi
}

main "$@"
