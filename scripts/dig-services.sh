#!/bin/bash
# show IP addresses for the services
    jq \
        --arg prod_conf "$(dig +short $(echo $(jq -r '.prod.conf' services-config.json) | sed 's|http[s]*://||'))" \
        --arg prod_dmsg_discovery "$(dig +short $(echo $(jq -r '.prod.dmsg_discovery' services-config.json) | sed 's|http[s]*://||'))" \
        --arg prod_transport_discovery "$(dig +short $(echo $(jq -r '.prod.transport_discovery' services-config.json) | sed 's|http[s]*://||'))" \
        --arg prod_address_resolver "$(dig +short $(echo $(jq -r '.prod.address_resolver' services-config.json) | sed 's|http[s]*://||'))" \
        --arg prod_route_finder "$(dig +short $(echo $(jq -r '.prod.route_finder' services-config.json) | sed 's|http[s]*://||'))" \
        --arg prod_uptime_tracker "$(dig +short $(echo $(jq -r '.prod.uptime_tracker' services-config.json) | sed 's|http[s]*://||'))" \
        --arg prod_service_discovery "$(dig +short $(echo $(jq -r '.prod.service_discovery' services-config.json) | sed 's|http[s]*://||'))" \
        --arg test_conf "$(dig +short $(echo $(jq -r '.test.conf' services-config.json) | sed 's|http[s]*://||'))" \
        --arg test_dmsg_discovery "$(dig +short $(echo $(jq -r '.test.dmsg_discovery' services-config.json) | sed 's|http[s]*://||'))" \
        --arg test_transport_discovery "$(dig +short $(echo $(jq -r '.test.transport_discovery' services-config.json) | sed 's|http[s]*://||'))" \
        --arg test_address_resolver "$(dig +short $(echo $(jq -r '.test.address_resolver' services-config.json) | sed 's|http[s]*://||'))" \
        --arg test_route_finder "$(dig +short $(echo $(jq -r '.test.route_finder' services-config.json) | sed 's|http[s]*://||'))" \
        --arg test_uptime_tracker "$(dig +short $(echo $(jq -r '.test.uptime_tracker' services-config.json) | sed 's|http[s]*://||'))" \
        --arg test_service_discovery "$(dig +short $(echo $(jq -r '.test.service_discovery' services-config.json) | sed 's|http[s]*://||'))" \
        '.prod.conf = $prod_conf |
         .prod.dmsg_discovery = $prod_dmsg_discovery |
         .prod.transport_discovery = $prod_transport_discovery |
         .prod.address_resolver = $prod_address_resolver |
         .prod.route_finder = $prod_route_finder |
         .prod.uptime_tracker = $prod_uptime_tracker |
         .prod.service_discovery = $prod_service_discovery |
         .test.conf = $test_conf |
         .test.dmsg_discovery = $test_dmsg_discovery |
         .test.transport_discovery = $test_transport_discovery |
         .test.address_resolver = $test_address_resolver |
         .test.route_finder = $test_route_finder |
         .test.uptime_tracker = $test_uptime_tracker |
         .test.service_discovery = $test_service_discovery' services-config.json
