# SSL certificate

This folder is for containing the SSL certificate that will be used by the dev server to to establish HTTPS connections.
The SSL certificate is ignored by GIT, so you will have to create one if you haven't done it before.

## Creating the certificate

This folder already has the configuration files needed for creating the certificate, so you just need to have OpenSSL
installed on your computer and run `generate.sh`. After that, 2 new files will be created: server.crt and server.key.

After that, you can just start the dev server. However, before that you may want to trust/install the certificate, to
make it possible to access the manager without having the browser displaying security warnings. The process for
trusting/installing  the SSL certificate is platform dependent.
