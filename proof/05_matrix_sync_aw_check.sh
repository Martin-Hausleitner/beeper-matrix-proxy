# Skip if user is actively using the Mac (ActivityWatch AFK check)
_aw_afk_status() {
  curl -sf --max-time 3 \
    "http://localhost:5600/api/0/buckets/aw-watcher-afk_MHs-MacBook-Pro.local/events?limit=1" \
    2>/dev/null | python3 -c "
import sys, json
evts = json.load(sys.stdin)
if evts and evts[0]['data'].get('status') == 'not-afk':
    # active within last 5 min?
    import datetime
    ts = evts[0]['timestamp'].replace('Z','+00:00')
    age = (datetime.datetime.now(datetime.timezone.utc) - datetime.datetime.fromisoformat(ts)).total_seconds()
    print('active' if age < 300 else 'afk')
else:
    print('afk')
" 2>/dev/null || echo "unknown"
}
_aw_status="$(_aw_afk_status)"
if [[ "$_aw_status" == "active" ]]; then
  echo "matrix-archive-sync: user is active (ActivityWatch), skipping run at $(date '+%H:%M')"
  exit 0
