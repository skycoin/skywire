#!/bin/bash
# update dmsghttp-config.json
    jq \
        --argjson prod_servers "$(curl -sL $(jq -r '.prod.dmsg_discovery' services-config.json)/dmsg-discovery/all_servers | jq -r 'map({(.static): .server.address}) | add')" \
        --argjson test_servers "$(curl -sL $(jq -r '.test.dmsg_discovery' services-config.json)/dmsg-discovery/all_servers | jq -r 'map({(.static): .server.address}) | add')" \
        --arg prod_dmsg_discovery "$(curl -sL $(jq -r '.prod.dmsg_discovery' services-config.json)/health | jq -r '.dmsg_address')" \
        --arg prod_transport_discovery "$(curl -sL $(jq -r '.prod.transport_discovery' services-config.json)/health | jq -r '.dmsg_address')" \
        --arg prod_address_resolver "$(curl -sL $(jq -r '.prod.address_resolver' services-config.json)/health | jq -r '.dmsg_address')" \
        --arg prod_route_finder "$(curl -sL $(jq -r '.prod.route_finder' services-config.json)/health | jq -r '.dmsg_address')" \
        --arg prod_uptime_tracker "$(curl -sL $(jq -r '.prod.uptime_tracker' services-config.json)/health | jq -r '.dmsg_address')" \
        --arg prod_service_discovery "$(curl -sL $(jq -r '.prod.service_discovery' services-config.json)/health | jq -r '.dmsg_address')" \
        --arg test_dmsg_discovery "$(curl -sL $(jq -r '.test.dmsg_discovery' services-config.json)/health | jq -r '.dmsg_address')" \
        --arg test_transport_discovery "$(curl -sL $(jq -r '.test.transport_discovery' services-config.json)/health | jq -r '.dmsg_address')" \
        --arg test_address_resolver "$(curl -sL $(jq -r '.test.address_resolver' services-config.json)/health | jq -r '.dmsg_address')" \
        --arg test_route_finder "$(curl -sL $(jq -r '.test.route_finder' services-config.json)/health | jq -r '.dmsg_address')" \
        --arg test_uptime_tracker "$(curl -sL $(jq -r '.test.uptime_tracker' services-config.json)/health | jq -r '.dmsg_address')" \
        --arg test_service_discovery "$(curl -sL $(jq -r '.test.service_discovery' services-config.json)/health | jq -r '.dmsg_address')" \
        '.prod.dmsg_servers |= map(if $prod_servers[.static] then .server.address = $prod_servers[.static] else . end) |
         .test.dmsg_servers |= map(if $test_servers[.static] then .server.address = $test_servers[.static] else . end) |
         .prod.dmsg_discovery = "dmsg://\($prod_dmsg_discovery)" |
         .prod.transport_discovery = "dmsg://\($prod_transport_discovery)" |
         .prod.address_resolver = "dmsg://\($prod_address_resolver)" |
         .prod.route_finder = "dmsg://\($prod_route_finder)" |
         .prod.uptime_tracker = "dmsg://\($prod_uptime_tracker)" |
         .prod.service_discovery = "dmsg://\($prod_service_discovery)" |
         .test.dmsg_discovery = "dmsg://\($test_dmsg_discovery)" |
         .test.transport_discovery = "dmsg://\($test_transport_discovery)" |
         .test.address_resolver = "dmsg://\($test_address_resolver)" |
         .test.route_finder = "dmsg://\($test_route_finder)" |
         .test.uptime_tracker = "dmsg://\($test_uptime_tracker)" |
         .test.service_discovery = "dmsg://\($test_service_discovery)"' dmsghttp-config.json
