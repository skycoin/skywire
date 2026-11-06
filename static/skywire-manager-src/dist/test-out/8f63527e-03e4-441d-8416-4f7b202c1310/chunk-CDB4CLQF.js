// src/app/utils/reward-address.ts
function rewardAddressValidator(acceptRisk) {
  return (c) => {
    const v = (c.value || "").trim();
    if (!v || v.startsWith("xpub") || acceptRisk()) {
      return null;
    }
    return { xpubRequired: true };
  };
}
function needsAcceptRisk(address, acceptRisk) {
  const v = (address || "").trim();
  return acceptRisk && !!v && !v.startsWith("xpub");
}

export {
  rewardAddressValidator,
  needsAcceptRisk
};
//# debugId=baea7dbf-6fe9-54f5-8dc0-ce90993fb3e7
//# sourceMappingURL=chunk-CDB4CLQF.js.map
