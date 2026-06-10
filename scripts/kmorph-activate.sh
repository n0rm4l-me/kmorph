#!/usr/bin/env bash
# kmorph-activate.sh — create a ProfileActivation for a ClusterProfile
# Usage: kmorph-activate.sh <profileName> [duration]
# Examples:
#   kmorph-activate.sh sleep           # indefinite, priority 50
#   kmorph-activate.sh production 4h   # expires in 4h

set -euo pipefail

PROFILE="${1:?Usage: kmorph-activate.sh <profileName> [duration]}"
DURATION="${2:-}"
PRIORITY=50
NAME="${PROFILE}-$(date +%s)"

MANIFEST="apiVersion: config.kmorph.io/v1alpha1
kind: ProfileActivation
metadata:
  name: ${NAME}
spec:
  profileRef: ${PROFILE}
  priority: ${PRIORITY}"

if [ -n "$DURATION" ]; then
  MANIFEST="${MANIFEST}
  duration: \"${DURATION}\""
fi

echo "$MANIFEST" | kubectl apply -f -
echo ""
echo "Created: ${NAME}  (profile=${PROFILE}, priority=${PRIORITY}${DURATION:+, duration=${DURATION}})"
echo "Delete to revert: kubectl delete profileactivation ${NAME}"
