package qdc507

// These scripts contain no configuration values or SIM credentials. ADB adds
// the boot-identity check before every invocation. Do not unload DSP modules:
// callbacks may still point into them even after a failed preparation.
const compatibilityScript = `
test "$(id -u)" = 0 || { echo 'ADB requires module root'; exit 77; }
test "$(uname -r)" = '3.18.44' || { echo 'module kernel mismatch'; exit 78; }
test "$(uname -m)" = 'armv7l' || { echo 'module architecture mismatch'; exit 78; }
test -x /usr/bin/alsaucm_test && test -x /usr/bin/amix && test -c /dev/ttyGS0 && test -p /run/voc_svr || exit 69
command -v timeout >/dev/null || exit 69
timeout -t 1 true || { echo 'unsupported module timeout syntax'; exit 69; }
`

const installationStateScript = `
if grep -q '^qdc507_afe ' /proc/modules; then echo 'legacy driver resident; reboot module'; exit 78; fi
if grep -q '^qdc507_voice ' /proc/modules || grep -q '^qdc507_aprv3 ' /proc/modules; then
  test -f ` + remoteDirectory + `/installed || { echo 'unowned or partial driver installation; reboot module'; exit 78; }
  test "$(cat ` + remoteDirectory + `/installed)" = "$(cat /proc/sys/kernel/random/boot_id)" || exit 78
  grep -q '^qdc507_voice ' /proc/modules && grep -q '^qdc507_aprv3 ' /proc/modules || exit 78
  echo installed
else
  test ! -L ` + remoteDirectory + ` || exit 77
  mkdir -p ` + remoteDirectory + ` && chmod 700 ` + remoteDirectory + ` || exit 73
  echo missing
fi
`

const soundReadyScript = `
n=0
while test "$n" -lt 100; do
  if test -c /dev/snd/controlC0 && test -c /dev/snd/pcmC0D4p && test -c /dev/snd/pcmC0D4c && test -c /dev/snd/pcmC0D5p && test -c /dev/snd/pcmC0D6c; then break; fi
  sleep 0.2; n=$((n+1))
done
test "$n" -lt 100 || { echo 'sound devices did not appear'; exit 69; }
grep -q 'mdm9607-tomtom-i2s-snd-card' /proc/asound/cards || exit 78
` + remoteDirectory + `/mavo-pcm-bridge.armv7 --check
`

const loadScript = `
chmod 700 ` + remoteDirectory + `/mavo-pcm-bridge.armv7 || exit 73
insmod ` + remoteDirectory + `/qdc507_aprv3.ko || exit 70
insmod ` + remoteDirectory + `/qdc507_voice.ko || exit 70
` + soundReadyScript + `
test "$?" = 0 || exit 70
cat /proc/sys/kernel/random/boot_id > ` + remoteDirectory + `/installed
`

// Ownership includes PID, process start time and executable. A stale PID never
// authorizes a signal to a newly reused process, including after service restart.
const ownershipFunctions = `
owned() {
  test -f "$record" || return 1
  read pid started < "$record" || return 1
  case "$pid:$started" in *[!0-9:]*|:*|*:) return 1;; esac
  test "$(cut -d ' ' -f 22 /proc/$pid/stat 2>/dev/null)" = "$started" || return 1
  test "$(tr '\000' '\n' < /proc/$pid/cmdline 2>/dev/null | head -1)" = "$program"
}
record_process() {
  started=$(cut -d ' ' -f 22 /proc/$pid/stat) || return 1
  printf '%s %s\n' "$pid" "$started" > "$record"
}
`

const calibrationScript = ownershipFunctions + `
record=` + remoteDirectory + `/calibration.pid
program=/usr/bin/alsaucm_test
if owned && grep -q 'ACDB -> Sent VocProc Cal!' ` + remoteDirectory + `/calibration.log; then
  tail -80 ` + remoteDirectory + `/calibration.log
  exit 0
fi
if ! owned; then
  for proc in /proc/[0-9]*/cmdline; do
    test "$(tr '\000' '\n' < "$proc" 2>/dev/null | head -1)" != "$program" || { echo 'calibration process belongs to another owner'; exit 73; }
  done
  test ! -p /run/alsaucm_test || rm /run/alsaucm_test || exit 73
  nohup "$program" </dev/null > ` + remoteDirectory + `/calibration.log 2>&1 &
  pid=$!; record_process || exit 73
fi
n=0
while test ! -p /run/alsaucm_test && owned && test "$n" -lt 50; do sleep 0.2; n=$((n+1)); done
test -p /run/alsaucm_test && owned || { echo 'calibration FIFO unavailable'; exit 69; }
for line in 'open snd_soc_msm_9x07_Tomtom_I2S' 'set _verb VoLTE' 'set _enadev Auxpcm Rx' 'set _enadev Auxpcm Tx'; do
  timeout -t 3 sh -c 'printf "%s\n" "$1" > /run/alsaucm_test' sh "$line" || exit 70
done
n=0
while ! grep -q 'ACDB -> Sent VocProc Cal!' ` + remoteDirectory + `/calibration.log && owned && test "$n" -lt 50; do sleep 0.2; n=$((n+1)); done
tail -80 ` + remoteDirectory + `/calibration.log
owned && grep -q 'ACDB -> Sent VocProc Cal!' ` + remoteDirectory + `/calibration.log
`
