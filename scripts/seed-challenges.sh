#!/usr/bin/env bash
# Upload the starter challenges through the API, as the admin user.
# Handy after a fresh deploy (a new cluster starts with an empty database).
#
#   ./scripts/seed-challenges.sh http://localhost:8081 <admin-password>    # kind
#   ./scripts/seed-challenges.sh http://<GKE-IP> <admin-password>          # GKE
#
# Print the admin password with:
#   kubectl -n ctf get secret ctf-secrets -o jsonpath='{.data.ADMIN_PASSWORD}' | base64 -d
#
# Running it twice creates duplicates, so run it once per fresh database.
set -euo pipefail

URL="${1:?usage: seed-challenges.sh <base-url> <admin-password>}"
PASS="${2:?usage: seed-challenges.sh <base-url> <admin-password>}"

# Log in and pull the JWT out of the JSON reply ({"token":"...","user":...}).
TOKEN="$(curl -sf -H 'Content-Type: application/json' \
  -d "{\"username\":\"admin\",\"password\":\"$PASS\"}" \
  "$URL/api/auth/login" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')"
[ -n "$TOKEN" ] || { echo "Login failed - check the URL and admin password." >&2; exit 1; }

# One challenge per line, as JSON: title, category, points, description, flag.
# (\n inside a description is a line break on the challenge page.)
while IFS= read -r challenge; do
  [ -z "$challenge" ] && continue
  # -o /dev/null -w code: print only the HTTP status (201 = created)
  code="$(curl -s -o /dev/null -w '%{http_code}' \
    -H 'Content-Type: application/json' \
    -H "Authorization: Bearer $TOKEN" \
    -d "$challenge" "$URL/api/challenges")"
  title="$(printf '%s' "$challenge" | sed -n 's/.*"title":"\([^"]*\)".*/\1/p')"
  echo "$code  $title"
done <<'EOF'
{"title":"Sanity Check","category":"misc","points":10,"description":"Welcome! Every flag looks like flag{...}.\n\nHere is your first one for free: flag{w3lc0me_t0_th3_ctf}","flag":"flag{w3lc0me_t0_th3_ctf}"}
{"title":"Backwards","category":"misc","points":25,"description":"Something about this flag looks off:\n\n}gn1r33n1gn3_3sr3v3r{galf","flag":"flag{r3v3rs3_3ng1n33r1ng}"}
{"title":"Count It Up","category":"misc","points":50,"description":"What is 1 + 2 + 3 + ... + 1000?\n\nSubmit the answer as flag{number}, e.g. flag{123}.","flag":"flag{500500}"}
{"title":"Not Encryption","category":"crypto","points":50,"description":"Someone said this is encrypted. It isn't.\n\nZmxhZ3tiNHNlNjRfaXNfbjB0X2VuY3J5cHQxMG59","flag":"flag{b4se64_is_n0t_encrypt10n}"}
{"title":"Caesar's Salad","category":"crypto","points":75,"description":"Julius rotated every letter by 13 places:\n\nsynt{p43fne_f4ynq}","flag":"flag{c43sar_s4lad}"}
{"title":"Hex Marks the Spot","category":"crypto","points":75,"description":"666c61677b6833785f6d34726b735f7468335f737030747d","flag":"flag{h3x_m4rks_th3_sp0t}"}
{"title":"Ones and Zeros","category":"crypto","points":100,"description":"01100110 01101100 01100001 01100111 01111011 00110000 01101110 00110011 01110011 01011111 00110100 01101110 01100100 01011111 01111010 00110011 01110010 00110000 01110011 01111101","flag":"flag{0n3s_4nd_z3r0s}"}
{"title":"Don't Look Here","category":"web","points":100,"description":"Web crawlers get told which pages to stay away from. Maybe you should take a look at that list on this site.","flag":"flag{r0b0ts_kn0w_th1ngs}"}
EOF

echo "Done. 201 = created."
