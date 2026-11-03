import { isRemoteVisor, nodePaletteTabs, nodeTabsData } from './node-tabs';

const local = '02' + 'a'.repeat(64);
const remote = '03' + 'b'.repeat(64);

const hasChat = (tabs: { label: string }[]) => tabs.some(t => t.label === 'node.tabs.chat');

describe('node tabs for a remote visor', () => {
  it('knows a remote visor from the local one', () => {
    expect(isRemoteVisor(remote, local)).toBeTrue();
    expect(isRemoteVisor(local, local)).toBeFalse();
    // Before /api/about answers nothing counts as remote.
    expect(isRemoteVisor(remote, undefined)).toBeFalse();
    expect(isRemoteVisor(undefined, local)).toBeFalse();
  });

  it('drops the Chat tab only for a remote visor', () => {
    expect(hasChat(nodeTabsData(local, false))).toBeTrue();
    expect(hasChat(nodeTabsData(remote, false, true))).toBeFalse();
    expect(hasChat(nodePaletteTabs(remote, false, true))).toBeFalse();
    expect(nodeTabsData(remote, false, true).length).toBe(nodeTabsData(remote, false).length - 1);
  });
});
