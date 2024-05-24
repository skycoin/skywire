# Route Finder

The *Route Finder* (or *Route Finding Service*) is responsible for finding and suggesting routes between two Skywire Nodes (identified by public keys). It is expected that an *App Node* is to use this service to find possible routes before contacting the *Setup Node*.

In the initial version of the *Route Finder*, it should use a basic algorithm to choose and order the best routes. This algorithm should find the x amount (limited by the max routes parameter) of "fastest" routes determined by the least amount of hops needed, and order it by hops ascending.

The implementation of *Route Finder* requires only a single REST API endpoint.

## Graph Algorithm

In order to explore routing we need to create a graph that represents the current skywire network, or at least the network formed by all the reachable nodes by `source node`.

For this purpose, we use the `mark and sweep` algorithm. Such an algorithm consists of two phases.

In the first phase, every object in the graph is explored in a `Deep First Search` order. This means that we need to explore every transport starting from route node, accessing the `transport-discovery` database each time that we need to retrieve new information.

In the second phase, we remove nodes from the graph that have not been visited, and then mark every node as unvisited in preparation for the next iteration of the algorithm.

An explanation and implementation of this algorithm can be found [here](https://www.geeksforgeeks.org/mark-and-sweep-garbage-collection-algorithm/).

## Routing algorithm

Given the previous graph we can now explore it to find the best routes from every given starting node to destiny node.

For this purpose we use a modification of `Dijkstra algorithm`.

An implementation can be found [here](http://rosettacode.org/wiki/Dijkstra%27s_algorithm#Go).

Route-finder modifies this algorithm by keeping track of all the nodes that reached to destination node. This allows the ability to backtrack every best route that arrives from a different node to destination node.

## Code Structure

The code should be in the `watercompany/skywire` repository;

- `/cmd/route-finder/route-finder.go` is the main executable for the *Route Finder*.
- `/pkg/route-finder/api/` contains the RESTFUL API definitions.
- `/pkg/route-finder/store/` contains the definition of the `Storer` interface and it's implementations [**TODO**].
- `/pkg/route-finder/client/` contains the client library that interacts with the *Route Finder* service's RESTFUL API.

## Database

The *Route Finder* only accesses the Transport database already defined in the *Transport Discovery* specification.

## Endpoint Definitions

All endpoint calls should include an `Accept: application/json` field in the request header, and the response header should include an `Content-Type: application/json` field.

### GET Routes available for the defined start and end key

Obtains the routes available for a specific start and end public key. Optionally with custom min and max hop parameters.

Note that each transport is to be represented by the `transport.Entry` structure.

**Request:**

```
GET /routes
```

```json
{
    "src_pk": "<src-pk>",
    "dst_pk": "<dst-pk>",
    "min_hops": 0,
    "max_hops": 0,
}
```

**Responses:**

- 200 OK (Success).
    ```json
    {
        "routes": [
            {
                "transports": [
                    {
                        "tid": "<tid>",
                        "edges": ["<initiator-pk>", "<responder-pk>"],
                        "type": "<type>",
                        "public": true,
                    }
                ]
            }
        ]
    }
    ```
- 400 Bad Request (Malformed request).
- 500 Internal Server Error (Server error).
