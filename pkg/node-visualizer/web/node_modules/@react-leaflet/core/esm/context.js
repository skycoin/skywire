import { createContext, useContext } from 'react';
export const CONTEXT_VERSION = 1;
export const LeafletContext = /*#__PURE__*/createContext(null);
export const LeafletProvider = LeafletContext.Provider;
export function useLeafletContext() {
  const context = useContext(LeafletContext);

  if (context == null) {
    throw new Error('No context provided: useLeafletContext() can only be used in a descendant of <MapContainer>');
  }

  return context;
}