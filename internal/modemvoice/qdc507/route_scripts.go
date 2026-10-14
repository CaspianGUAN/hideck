package qdc507

const checkRouteScript = ownershipFunctions + `
record=` + remoteDirectory + `/route.pid
program=` + remoteDirectory + `/mavo-pcm-bridge.armv7
owned && test "$(cat /sys/class/android_usb/f_audio/audio_enable)" = 1 &&
grep -q '^state: RUNNING' /proc/asound/card0/pcm4p/sub0/status &&
grep -q '^state: RUNNING' /proc/asound/card0/pcm4c/sub0/status
`

const startRouteScript = ownershipFunctions + `
record=` + remoteDirectory + `/route.pid
program=` + remoteDirectory + `/mavo-pcm-bridge.armv7
if owned; then echo 'existing route requires cleanup'; exit 73; fi
for proc in /proc/[0-9]*/cmdline; do
  case "$(tr '\000' '\n' < "$proc" 2>/dev/null | head -1)" in
    */mavo-pcm-bridge.armv7) echo 'unowned audio helper is running'; exit 73;;
  esac
done
test "$(cat /sys/class/android_usb/f_audio/audio_enable)" = 0 || { echo 'USB audio is already enabled'; exit 73; }
nohup "$program" --voice-route-session --verbose </dev/null > ` + remoteDirectory + `/route.log 2>&1 &
pid=$!; record_process || exit 73
n=0
while owned && test "$n" -lt 50; do
  if grep -q 'VoLTE route session active on hw:0,4' ` + remoteDirectory + `/route.log &&
    test "$(cat /sys/class/android_usb/f_audio/audio_enable)" = 1 &&
    grep -q '^state: RUNNING' /proc/asound/card0/pcm4p/sub0/status &&
    grep -q '^state: RUNNING' /proc/asound/card0/pcm4c/sub0/status; then exit 0; fi
  sleep 0.2; n=$((n+1))
done
tail -50 ` + remoteDirectory + `/route.log
exit 69
`

const stopOwnedFunction = `
stop_owned() {
  if owned; then
    kill -TERM "$pid" || return 1
    n=0
    while owned && test "$n" -lt 50; do sleep 0.1; n=$((n+1)); done
    if owned; then echo 'owned process did not stop'; return 1; fi
  fi
}
`

const stopRouteScript = ownershipFunctions + stopOwnedFunction + `
record=` + remoteDirectory + `/route.pid
program=` + remoteDirectory + `/mavo-pcm-bridge.armv7
test -f "$record" || exit 0
stop_owned || exit 70
test "$(cat /sys/class/android_usb/f_audio/audio_enable)" = 0 || { echo 'audio helper did not restore USB route'; exit 70; }
test "$(cat /proc/asound/card0/pcm4p/sub0/status)" = closed && test "$(cat /proc/asound/card0/pcm4c/sub0/status)" = closed || exit 70
for line in T T B; do timeout -t 3 sh -c 'printf "%s\n" "$1" > /run/voc_svr' sh "$line" || exit 70; done
rm "$record"
`

const stopCalibrationScript = ownershipFunctions + stopOwnedFunction + `
record=` + remoteDirectory + `/calibration.pid
program=/usr/bin/alsaucm_test
test -f "$record" || exit 0
if owned; then
  timeout -t 3 sh -c 'printf "set _verb Inactive\n" > /run/alsaucm_test' || exit 70
  n=0
  while owned && test "$n" -lt 50; do
    if /usr/bin/amix 'SEC_AUX_PCM_RX_Voice Mixer VoLTE' 2>&1 | grep -q ': off$'; then
      if /usr/bin/amix 'VoLTE_Tx Mixer SEC_AUX_PCM_TX_VoLTE' 2>&1 | grep -q ': off$'; then break; fi
    fi
    sleep 0.1; n=$((n+1))
  done
  test "$n" -lt 50 && owned || { echo 'calibration did not disable voice mixers'; exit 70; }
fi
stop_owned || exit 70
/usr/bin/amix 'SEC_AUX_PCM_RX_Voice Mixer VoLTE' 2>&1 | grep -q ': off$' || { echo 'receive mixer remains enabled'; exit 70; }
/usr/bin/amix 'VoLTE_Tx Mixer SEC_AUX_PCM_TX_VoLTE' 2>&1 | grep -q ': off$' || { echo 'transmit mixer remains enabled'; exit 70; }
rm "$record"
`
