# `dmsg.Server`

## Endpoints

There is only one endpoint on port `:8082`. `:8082` is the default port for the httpserver and can be changed with the flag `-p`.

### GET Entry

Obtains the dmsg servers health data.

> `GET /health`

**RESPONSE**

Possible Status Codes:

- Success (200) - Successfully updated record.

  - Header:

    ```
    Content-Type: application/json
    ```

  - Body:

    > JSON-encoded entry.
    ```json
    {
      "build_info":{
        "version":"f43904c",
        "commit":"f43904c48981cf89e9e3066a942bf148b1412694",
        "date":"2021-08-26T09:53:40Z"
      },
      "started_at":"2021-08-26T15:24:15.485148635+05:30"
    }
    ```
