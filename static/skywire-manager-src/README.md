# Skywire Manager

Frontend application that allows to manage a group of Skywire nodes through a Hypervisor instance.

Note: The software is still under heavy development and the current version is intended for public testing purposes only.

## Prerequisites

The Skywire Manager requires Node 8.11.0 or higher, together with NPM 6.0.0 or higher.

## Initial configuration

Dependencies needed for this app are managed with NPM and are not included in the repository, so you must run the `npm install`
command on this folder before being able to tun the app.

Also, as the app needs a HTTPS connection to work, you may want to create a SSL certificate for the dev server. This step is
optional, as the dev server can create its own certificate, but there are 2 reasons for manually creating one: to avoid a bug
in the dev server that sometimes makes it to reload the app when it shouild not
(https://github.com/angular/angular-cli/issues/5826) and to have more freedom managing the trusted certificates. For creating
a custom certificate, follow the steps in the [ssl folder](./ssl/README.md)

## Hypervisor configuration

For the app to work, the Hypervisor instance must be running with authentication disabled or with a password set for the
"admin" account. For running the hypervisor instance without authentication, start it with the `enable_auth` configuration
value set to `false` in the Hypervisor configuration file or using the `-m` flag. For setting a password for the "admin"
account, use the `/create-account` API endpoint.

Also, the Hypervisor instance must be running in `http://localhost:8080`. If it is running in another URL, you can change it in
[proxy.config.json](proxy.config.json) before running the app.

## Running the app

Run `npm run start` to start a dev server. If you followed the steps indicated in the [ssl folder](./ssl/README.md), the server
will use your custom SSL certificate. If not, the server will use an automatically created one.

After the server is started, you can access the app by navigating to `http://localhost:4200` with a web browser (note
that you could get a security warning if the SSL certificate is not in the trusted certificates list). The app will
automatically reload if you change any of the source files.

## Build

Run `npm run build` to build the project. The build artifacts will be stored in the `dist/` directory.
