import { compareVersions, versionParts } from './node-list.component';

// The Outdated chip compares visor versions with these. A wrong order would
// flag current visors or hide old ones.
describe('node list version order', () => {
  it('parses tagged and post-tag versions', () => {
    expect(versionParts('v1.3.103')).toEqual([1, 3, 103, 0]);
    expect(versionParts('v1.3.103-122-g7845bbc81-7845bbc81358')).toEqual([1, 3, 103, 122]);
    expect(versionParts('1.3.104')).toEqual([1, 3, 104, 0]);
  });

  it('gives null for a build name that is not a version', () => {
    expect(versionParts('develop-latest-tinygo-cores-7845bbc81358')).toBeNull();
    expect(versionParts(undefined)).toBeNull();
  });

  it('orders by release, then by commits after the tag', () => {
    const order = ['v1.3.99', 'v1.3.103', 'v1.3.103-5-gabc', 'v1.3.103-122-gdef', 'v1.3.104', 'v1.4.0']
      .map(v => versionParts(v) as number[]);
    for (let i = 1; i < order.length; i++) {
      expect(compareVersions(order[i - 1], order[i])).toBeLessThan(0);
    }
    expect(compareVersions(order[2], order[2])).toBe(0);
  });
});
