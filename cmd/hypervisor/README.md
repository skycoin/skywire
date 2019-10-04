# Hypervisor

Hypervisor exposes node management operations via web API.

**Generate config file:**

```bash

```

**Run with mock data:**

```bash
# Generate config file.
$ hypervisor gen-config

# Run.
$ hypervisor --mock
```

Running `hypervisor` alone won't be useful, you will probably want to run multiple `visors` so they can communicate and you can start calling it's endpoints with results.
By default, the RESTful API is served on `:8080`.

## Running multiple visors

Refer to the `visor` README document to learn how to run them. On the visor config file there is a section called `hypervisors`, where you can define hypervisors that you want your visor to connect to. Each hypervisor is configured providing it's `public_key` and it's `port`.

The easiest way to run this all is by following instructions in `Running generic integration environment` below, but it is advised to read the rest of the guide before.

## Start using hypervisor

Now you can check whether the `visor` managed to communicate with it. However, the default `hypervisor` config has authentication enabled, and will require you to sign up a new user in order to get succesful replies from it.

For that purpose there is the endpoint `/api/create-account`, which accepts a json body in the form:

```json
{"username":"admin", "password":"123456"}
```

After creating the user you must login with `/api/login` using the same json payload than before. This will return an `swm-session` cookie that you must use on your subsequent requests in order for them to be accepted. With `/api/user` and providing the username you can see some information like your session ID and it's expiration time.

If you are testing the API, this process of carrying the session cookie might be tedious. In the configuration file you can shut down authentication by setting `enable_auth` value to `false`.

After you have authorized or turned it off, you can proceed to check whether there is communication between the hypervisor and the visors by calling `/api/nodes`. If your `visor` pk shows up here then they are communicating.

Other endpoints are documented in `skywire-mainnet/cmd/hypervisor/hypervisor.postman_collection.json`. While most of them are, currently some are not.

You can find which endpoints `hypervisor` is serving in it's totallity in `skywire-mainnet/pkg/hypervisor/hypervisor.go:L132` on.

## Running generic integration environment

If you find trouble following the aforementioned steps there is an easier way to run a `hypervisor` along with three `visors`, provided that you have access to `skywire-services` repository.

There are a few goals in it's makefile that allows you to run an interactive integration environment. You will need to have `skywire-mainnet` and `skywire-services` projects under the same directory. Then, `cd skywire-services`.

Here you must run `make integration-build`. Then `make integration-run-generic`. This last goal will open a tmux session with multiple tabs, one for each service. On tab 5 you will find the `hypervisor`. On tabs 6, 7 and 8, three `visors`. On tab 9 you can run commands.

Run `make integration-startup` in tab 9 to add transports between the nodes. If you want to setup a route between them the easiest way is to request `skychat` app to send a message from one visor to another:
`curl --data
{'"recipient":"'$PK_A'", "message":"Hello Joe!"}' -X POST  $CHAT_C`

When you are done you should run `make integration-teardown; tmux kill-server`.

Note that this hypervisor is not running authorized, so it lacks `/create-account`, `/login` and `/logout` endpoints. You can turn it on by modifying the config file under `skywire-services/integration/hypervisor.json`. Then re-run `make integration-run-generic` again.

## Endpoints Documentation

Endpoints are documented in the provided [Postman](https://www.getpostman.com/) file: `hypervisor.postman_collection.json`.
