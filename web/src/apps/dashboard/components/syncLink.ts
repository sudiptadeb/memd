// The deep link that opens Project B's Sync settings with a bucket's address
// filled in: its one parameter, `address`, is the single-address form
// https://<host>/sync/<token>, percent-encoded.
export function syncDeepLink(address: string): string {
  return "project-b://sync?address=" + encodeURIComponent(address);
}
