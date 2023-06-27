import { useMap } from './hooks';
export function MapConsumer({
  children
}) {
  return children(useMap());
}