/**
 * The address of a visor's terminal page, which the hypervisor serves at
 * pty/<pk>. It is resolved against the document base, so it keeps the desk's
 * /vnet/<port>/ prefix; from the dev server it points at the local hypervisor.
 */
export function ptyUrl(key: string, query = ''): string {
  const url = new URL('pty/' + key + query, document.baseURI);
  if (url.host === 'localhost:4200') {
    url.host = '127.0.0.1:8000';
  }

  return url.href;
}
