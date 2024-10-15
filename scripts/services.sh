#!/bin/bash
# update services-config.json
  jq --argjson test "$(curl -sL $(jq -r .test.conf services-config.json))" \
     --argjson prod "$(curl -sL $(jq -r .prod.conf services-config.json))" \
     '.test += $test | .prod += $prod' services-config.json
