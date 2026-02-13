// Country/IP/dual grouping, boundary enforcement, circle packing, and satellite orbit animation

import * as S from './state';
import { getCountryColor, getIPGroupColor, getIPGroupBorder, countryToFlag } from './utils';
import { SATELLITE_EMOJI, ORBIT_LANES, ORBIT_LANE_SPACING, ORBIT_ANGULAR_VELOCITY } from './constants';
import { updateLegend } from './sidebar';
import type { PlacedCircle, GroupBoundary, GroupCentroid } from './types';

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

/** Count cross-group connections for a node */
function countCrossGroupConnections(nodeId: string, getGroupKey: (id: string) => string): number {
    const connections = S.visorConnections[nodeId] || [];
    const myGroup = getGroupKey(nodeId);
    let crossCount = 0;
    for (const conn of connections) {
        const otherGroup = getGroupKey(conn.pk);
        if (otherGroup !== myGroup) {
            crossCount++;
        }
    }
    return crossCount;
}

/** Calculate radius needed for a group based on node count */
function calculateGroupRadius(nodeCount: number): number {
    const nodeArea = 2500; // ~50x50 per node with spacing
    return Math.max(80, Math.sqrt(nodeCount * nodeArea / Math.PI) + 40);
}

/** Check if a circle overlaps with any placed circles */
function circleOverlaps(
    x: number, y: number, radius: number,
    placedCircles: PlacedCircle[], padding: number,
): boolean {
    for (const placed of placedCircles) {
        const dx = x - placed.x;
        const dy = y - placed.y;
        const dist = Math.sqrt(dx * dx + dy * dy);
        const minDist = radius + placed.radius + padding;
        if (dist < minDist) {
            return true;
        }
    }
    return false;
}

/** Find a position for a circle that doesn't overlap with placed circles */
function findNonOverlappingPosition(
    radius: number, placedCircles: PlacedCircle[], padding: number,
): { x: number; y: number } {
    if (placedCircles.length === 0) {
        return { x: 0, y: 0 };
    }

    // Try concentric rings with multiple angles per ring
    const minSpacing = radius + padding;
    for (let ring = 1; ring <= 50; ring++) {
        const ringRadius = minSpacing * ring * 0.4;
        const numAngles = Math.max(8, Math.floor(ring * 4));

        for (let i = 0; i < numAngles; i++) {
            const angle = (i / numAngles) * 2 * Math.PI + (ring % 2) * (Math.PI / numAngles);
            const x = ringRadius * Math.cos(angle);
            const y = ringRadius * Math.sin(angle);

            if (!circleOverlaps(x, y, radius, placedCircles, padding)) {
                return { x, y };
            }
        }
    }

    // Fallback: use hexagonal packing
    const cols = Math.ceil(Math.sqrt(placedCircles.length + 1));
    const row = Math.floor(placedCircles.length / cols);
    const col = placedCircles.length % cols;
    const spacing = (radius + padding) * 2.5;
    const xOffset = (row % 2) * spacing * 0.5;
    return { x: col * spacing + xOffset, y: row * spacing * 0.866 };
}

/** Find non-overlapping position within a bounded circle */
function findNonOverlappingPositionWithin(
    radius: number,
    placed: PlacedCircle[],
    padding: number,
    cx: number,
    cy: number,
    maxDist: number,
): { x: number; y: number } {
    // Check center first
    if (!circleOverlaps(cx, cy, radius, placed, padding)) {
        return { x: cx, y: cy };
    }

    // Try concentric rings with multiple angles per ring
    const minSpacing = radius + padding;
    for (let ring = 1; ring <= 20; ring++) {
        const ringRadius = minSpacing * ring * 0.5;
        if (ringRadius + radius > maxDist) break;

        const numAngles = Math.max(8, Math.floor(ring * 6));
        for (let i = 0; i < numAngles; i++) {
            const angle = (i / numAngles) * 2 * Math.PI + (ring % 2) * (Math.PI / numAngles);
            const x = cx + ringRadius * Math.cos(angle);
            const y = cy + ringRadius * Math.sin(angle);

            const distFromCenter = Math.sqrt((x - cx) ** 2 + (y - cy) ** 2);
            if (distFromCenter + radius <= maxDist && !circleOverlaps(x, y, radius, placed, padding)) {
                return { x, y };
            }
        }
    }

    // Fallback: find the position with minimum overlap
    let bestPos = { x: cx, y: cy };
    let bestScore = Infinity;

    for (let angle = 0; angle < 2 * Math.PI; angle += Math.PI / 8) {
        for (let dist = 0; dist <= maxDist - radius; dist += 20) {
            const x = cx + dist * Math.cos(angle);
            const y = cy + dist * Math.sin(angle);

            let score = 0;
            for (const p of placed) {
                const d = Math.sqrt((x - p.x) ** 2 + (y - p.y) ** 2);
                const minD = radius + p.radius + padding;
                if (d < minD) {
                    score += (minD - d) * 10;
                }
            }

            if (score < bestScore) {
                bestScore = score;
                bestPos = { x, y };
            }
        }
    }

    return bestPos;
}

/** Calculate convex hull of a set of points (Graham scan algorithm) */
function convexHull(points: { x: number; y: number }[]): { x: number; y: number }[] {
    if (points.length < 3) return points;

    // Find the lowest point (and leftmost if tied)
    let lowest = 0;
    for (let i = 1; i < points.length; i++) {
        if (points[i].y > points[lowest].y ||
            (points[i].y === points[lowest].y && points[i].x < points[lowest].x)) {
            lowest = i;
        }
    }
    [points[0], points[lowest]] = [points[lowest], points[0]];
    const pivot = points[0];

    // Sort by polar angle with pivot
    const sorted = points.slice(1).sort((a, b) => {
        const angleA = Math.atan2(a.y - pivot.y, a.x - pivot.x);
        const angleB = Math.atan2(b.y - pivot.y, b.x - pivot.x);
        return angleA - angleB;
    });

    const hull: { x: number; y: number }[] = [pivot];
    for (const p of sorted) {
        while (hull.length > 1) {
            const a = hull[hull.length - 2];
            const b = hull[hull.length - 1];
            const cross = (b.x - a.x) * (p.y - a.y) - (b.y - a.y) * (p.x - a.x);
            if (cross <= 0) hull.pop();
            else break;
        }
        hull.push(p);
    }
    return hull;
}

/** Expand hull points outward by padding amount */
function expandHull(hull: { x: number; y: number }[], padding: number): { x: number; y: number }[] {
    if (hull.length < 3) {
        if (hull.length === 1) {
            const p = hull[0];
            const r = padding;
            return [
                { x: p.x - r, y: p.y - r },
                { x: p.x + r, y: p.y - r },
                { x: p.x + r, y: p.y + r },
                { x: p.x - r, y: p.y + r },
            ];
        } else {
            const p1 = hull[0], p2 = hull[1];
            const dx = p2.x - p1.x, dy = p2.y - p1.y;
            const len = Math.sqrt(dx * dx + dy * dy) || 1;
            const nx = -dy / len * padding, ny = dx / len * padding;
            return [
                { x: p1.x + nx - dx / len * padding, y: p1.y + ny - dy / len * padding },
                { x: p2.x + nx + dx / len * padding, y: p2.y + ny + dy / len * padding },
                { x: p2.x - nx + dx / len * padding, y: p2.y - ny + dy / len * padding },
                { x: p1.x - nx - dx / len * padding, y: p1.y - ny - dy / len * padding },
            ];
        }
    }

    // Calculate centroid
    let cx = 0, cy = 0;
    for (const p of hull) { cx += p.x; cy += p.y; }
    cx /= hull.length; cy /= hull.length;

    // Expand each point outward from centroid
    return hull.map(p => {
        const dx = p.x - cx, dy = p.y - cy;
        const dist = Math.sqrt(dx * dx + dy * dy) || 1;
        return {
            x: p.x + (dx / dist) * padding,
            y: p.y + (dy / dist) * padding,
        };
    });
}

// ---------------------------------------------------------------------------
// Satellite orbit functions
// ---------------------------------------------------------------------------

/** Calculate orbit parameters based on placed country circles */
function calculateOrbitParams(placedCountries: PlacedCircle[]): void {
    if (placedCountries.length === 0) {
        S.setOrbitCenter({ x: 0, y: 0 });
        S.setOrbitBaseRadius(500);
        return;
    }
    let cx = 0, cy = 0;
    for (const c of placedCountries) { cx += c.x; cy += c.y; }
    cx /= placedCountries.length;
    cy /= placedCountries.length;
    S.setOrbitCenter({ x: cx, y: cy });

    let maxExtent = 0;
    for (const c of placedCountries) {
        const dist = Math.sqrt((c.x - cx) ** 2 + (c.y - cy) ** 2) + c.radius;
        if (dist > maxExtent) maxExtent = dist;
    }
    S.setOrbitBaseRadius(maxExtent + 150);
}

/** Initialize satellite orbits for unknown-country nodes */
function initializeSatelliteOrbits(nodeIds: string[]): void {
    S.satelliteOrbits.clear();
    S.setSatelliteNodeIds(new Set(nodeIds));
    if (nodeIds.length === 0) return;

    const angleStep = (2 * Math.PI) / nodeIds.length;
    nodeIds.forEach((id, i) => {
        const lane = i % ORBIT_LANES;
        S.satelliteOrbits.set(id, {
            angle: i * angleStep,
            lane: lane,
            emoji: SATELLITE_EMOJI,
            isDragged: false,
        });
    });
}

/** Apply satellite visual styles to nodes (emoji appearance) */
function applySatelliteStyles(): void {
    const updates: any[] = [];
    S.satelliteOrbits.forEach((orbit, id) => {
        updates.push({
            id: id,
            shape: 'text',
            label: orbit.emoji,
            font: { size: 20, color: '#ffffff' },
        });
    });
    if (updates.length > 0) S.nodesDataset!.update(updates);
}

/** Animation loop for satellite orbits */
function animateSatelliteOrbit(): void {
    if (S.satelliteNodeIds.size === 0) return;

    const updates: { id: string; x: number; y: number }[] = [];
    S.satelliteOrbits.forEach((orbit, id) => {
        if (orbit.isDragged) return;
        orbit.angle += ORBIT_ANGULAR_VELOCITY;
        const r = S.orbitBaseRadius + orbit.lane * ORBIT_LANE_SPACING;
        const x = S.orbitCenter.x + r * Math.cos(orbit.angle);
        const y = S.orbitCenter.y + r * Math.sin(orbit.angle);
        updates.push({ id: id, x: x, y: y });
    });
    if (updates.length > 0) S.nodesDataset!.update(updates);

    S.setSatelliteOrbitAnimId(requestAnimationFrame(animateSatelliteOrbit));
}

function startSatelliteOrbit(): void {
    stopSatelliteOrbit();
    if (S.satelliteNodeIds.size > 0) {
        S.setSatelliteOrbitAnimId(requestAnimationFrame(animateSatelliteOrbit));
    }
}

function stopSatelliteOrbit(): void {
    if (S.satelliteOrbitAnimId !== null) {
        cancelAnimationFrame(S.satelliteOrbitAnimId);
        S.setSatelliteOrbitAnimId(null);
    }
}

/** Restore satellite nodes to normal appearance */
function clearSatelliteNodes(): void {
    if (S.satelliteNodeIds.size === 0) return;
    stopSatelliteOrbit();

    const updates: any[] = [];
    S.satelliteNodeIds.forEach(id => {
        const node = S.nodesDataset!.get(id);
        if (!node) return;
        updates.push({
            id: id,
            shape: 'dot',
            label: id.substring(0, 8),
            font: { size: 10, color: '#aaa' },
        });
    });
    if (updates.length > 0) S.nodesDataset!.update(updates);

    S.satelliteNodeIds.clear();
    S.satelliteOrbits.clear();
}

// ---------------------------------------------------------------------------
// Node placement helpers
// ---------------------------------------------------------------------------

/** Place nodes within a circular area using sunflower pattern */
function placeNodesInCircle(
    nodeIds: string[],
    centroid: { x: number; y: number; radius: number },
    updates: { id: string; x: number; y: number }[],
): void {
    const count = nodeIds.length;
    const innerRadius = centroid.radius - 40;

    nodeIds.forEach((nodeId, i) => {
        let x: number, y: number;
        if (count === 1) {
            x = centroid.x;
            y = centroid.y;
        } else if (count <= 6) {
            const angle = (i / count) * 2 * Math.PI;
            const r = innerRadius * 0.5;
            x = centroid.x + r * Math.cos(angle);
            y = centroid.y + r * Math.sin(angle);
        } else {
            const goldenAngle = Math.PI * (3 - Math.sqrt(5));
            const angle = i * goldenAngle;
            const r = innerRadius * Math.sqrt(i / count) * 0.9;
            x = centroid.x + r * Math.cos(angle);
            y = centroid.y + r * Math.sin(angle);
        }
        updates.push({ id: nodeId, x: x, y: y });
    });
}

/** Scatter nodes within a circle, avoiding placed sub-circles */
function placeNodesScattered(
    nodeIds: string[],
    countryCircle: { x: number; y: number; radius: number },
    placedSubs: PlacedCircle[],
    updates: { id: string; x: number; y: number }[],
): void {
    const count = nodeIds.length;
    const margin = 50;
    let placed = 0;
    let attempts = 0;

    while (placed < count && attempts < count * 50) {
        const angle = Math.random() * 2 * Math.PI;
        const r = Math.random() * (countryCircle.radius - margin);
        const x = countryCircle.x + r * Math.cos(angle);
        const y = countryCircle.y + r * Math.sin(angle);

        let valid = true;
        for (const sub of placedSubs) {
            const dist = Math.sqrt((x - sub.x) ** 2 + (y - sub.y) ** 2);
            if (dist < sub.radius + 30) {
                valid = false;
                break;
            }
        }

        if (valid) {
            updates.push({ id: nodeIds[placed], x: x, y: y });
            placed++;
        }
        attempts++;
    }

    // Fallback: place remaining nodes at edge of country
    while (placed < count) {
        const angle = (placed / count) * 2 * Math.PI;
        const r = countryCircle.radius - margin;
        const x = countryCircle.x + r * Math.cos(angle);
        const y = countryCircle.y + r * Math.sin(angle);
        updates.push({ id: nodeIds[placed], x: x, y: y });
        placed++;
    }
}

/** Place nodes outside all placed circles */
function placeNodesOutside(
    nodeIds: string[],
    placedCircles: PlacedCircle[],
    updates: { id: string; x: number; y: number }[],
): void {
    if (placedCircles.length === 0) {
        nodeIds.forEach((nodeId, i) => {
            const angle = (i / nodeIds.length) * 2 * Math.PI;
            const r = 200 + i * 30;
            updates.push({ id: nodeId, x: r * Math.cos(angle), y: r * Math.sin(angle) });
        });
        return;
    }

    // Find bounding box of all circles
    let minX = Infinity, maxX = -Infinity, minY = Infinity, maxY = -Infinity;
    for (const c of placedCircles) {
        minX = Math.min(minX, c.x - c.radius);
        maxX = Math.max(maxX, c.x + c.radius);
        minY = Math.min(minY, c.y - c.radius);
        maxY = Math.max(maxY, c.y + c.radius);
    }

    // Place nodes in a band outside the bounding box
    const padding = 150;
    nodeIds.forEach((nodeId, i) => {
        const side = i % 4;
        const offset = Math.floor(i / 4) * 60;
        let x: number = 0, y: number = 0;
        switch (side) {
            case 0: x = maxX + padding + offset; y = minY + (i * 50) % (maxY - minY); break;
            case 1: x = minX - padding - offset; y = minY + (i * 50) % (maxY - minY); break;
            case 2: x = minX + (i * 50) % (maxX - minX); y = maxY + padding + offset; break;
            case 3: x = minX + (i * 50) % (maxX - minX); y = minY - padding - offset; break;
        }
        updates.push({ id: nodeId, x: x, y: y });
    });
}

/** Place IP sub-circles within a country circle using Fibonacci/Vogel spiral */
function placeFibonacciSpiral(
    ipGroups: { ipKey: string; nodeIds: string[]; radius: number }[],
    cc: { x: number; y: number; radius: number },
): PlacedCircle[] {
    const GOLDEN_ANGLE = Math.PI * (3 - Math.sqrt(5)); // ~137.508 degrees
    const subPadding = 20;

    if (ipGroups.length === 0) return [];

    const positions: PlacedCircle[] = [];

    // First (largest) circle goes at center
    positions.push({ x: cc.x, y: cc.y, radius: ipGroups[0].radius });

    if (ipGroups.length === 1) return positions;

    // Estimate initial scaling factor based on average sub-circle size
    const avgRadius = ipGroups.reduce((s, g) => s + g.radius, 0) / ipGroups.length;
    let scaleFactor = (avgRadius + subPadding) * 0.85;

    for (let i = 1; i < ipGroups.length; i++) {
        const subRadius = ipGroups[i].radius;
        let placed = false;

        // Walk along the Vogel spiral testing candidate positions
        for (let n = i; n < i + 200; n++) {
            const angle = n * GOLDEN_ANGLE;
            const dist = scaleFactor * Math.sqrt(n);
            const x = cc.x + dist * Math.cos(angle);
            const y = cc.y + dist * Math.sin(angle);

            // Check within country boundary
            const distFromCenter = Math.sqrt((x - cc.x) ** 2 + (y - cc.y) ** 2);
            if (distFromCenter + subRadius > cc.radius - 30) continue;

            // Check overlap with already-placed sub-circles
            let overlaps = false;
            for (const p of positions) {
                const d = Math.sqrt((x - p.x) ** 2 + (y - p.y) ** 2);
                if (d < subRadius + p.radius + subPadding) {
                    overlaps = true;
                    break;
                }
            }

            if (!overlaps) {
                positions.push({ x, y, radius: subRadius });
                placed = true;
                break;
            }
        }

        // Fallback: if spiral couldn't place it, try with increased scale
        if (!placed) {
            scaleFactor *= 1.15;
            for (let n = 1; n < 500; n++) {
                const angle = n * GOLDEN_ANGLE;
                const dist = scaleFactor * Math.sqrt(n);
                const x = cc.x + dist * Math.cos(angle);
                const y = cc.y + dist * Math.sin(angle);

                const distFromCenter = Math.sqrt((x - cc.x) ** 2 + (y - cc.y) ** 2);
                if (distFromCenter + subRadius > cc.radius - 20) continue;

                let overlaps = false;
                for (const p of positions) {
                    const d = Math.sqrt((x - p.x) ** 2 + (y - p.y) ** 2);
                    if (d < subRadius + p.radius + subPadding) {
                        overlaps = true;
                        break;
                    }
                }

                if (!overlaps) {
                    positions.push({ x, y, radius: subRadius });
                    placed = true;
                    break;
                }
            }
        }

        // Ultimate fallback: find position with minimum overlap
        if (!placed) {
            let bestPos = { x: cc.x, y: cc.y };
            let bestOverlap = Infinity;

            // Try many positions to find one with least overlap
            for (let angle = 0; angle < 2 * Math.PI; angle += Math.PI / 16) {
                for (let dist = 0; dist <= cc.radius - subRadius - 10; dist += 15) {
                    const x = cc.x + dist * Math.cos(angle);
                    const y = cc.y + dist * Math.sin(angle);

                    let totalOverlap = 0;
                    for (const p of positions) {
                        const d = Math.sqrt((x - p.x) ** 2 + (y - p.y) ** 2);
                        const minD = subRadius + p.radius + subPadding;
                        if (d < minD) {
                            totalOverlap += minD - d;
                        }
                    }

                    if (totalOverlap < bestOverlap) {
                        bestOverlap = totalOverlap;
                        bestPos = { x, y };
                        if (totalOverlap === 0) break;
                    }
                }
                if (bestOverlap === 0) break;
            }

            positions.push({ x: bestPos.x, y: bestPos.y, radius: subRadius });
        }
    }

    return positions;
}

// ---------------------------------------------------------------------------
// Boundary enforcement (exported)
// ---------------------------------------------------------------------------

/** Hard boundary enforcement + gravity toward center for high-connection nodes */
export function startGroupBoundaryEnforcement(
    getGroupKey: (nodeId: string) => string,
    groupCentroids: Map<string, GroupCentroid>,
): void {
    stopGroupBoundaryEnforcement();
    S.setCurrentGroupKeyFn(getGroupKey);
    S.setFixedGroupCentroids(groupCentroids);

    // Pre-calculate cross-group connection counts
    const crossCounts = new Map<string, number>();
    let maxCross = 1;
    S.nodesDataset!.forEach((node: any) => {
        const count = countCrossGroupConnections(node.id, getGroupKey);
        crossCounts.set(node.id, count);
        if (count > maxCross) maxCross = count;
    });

    // Track heat per group using exponential moving average
    const groupHeat = new Map<string, { heat: number; baseRadius: number; nodeCount: number }>();
    const minNodeDist = 35;
    const heatDecay = 0.92;
    const heatThresholdHigh = 15;
    const heatThresholdLow = 3;

    groupCentroids.forEach((centroid, key) => {
        groupHeat.set(key, {
            heat: 0,
            baseRadius: centroid.radius,
            nodeCount: 0,
        });
    });

    S.setGroupForceInterval(setInterval(() => {
        if (!S.network || !S.groupingMode || !S.fixedGroupCentroids) return;

        const positions = S.network.getPositions();
        const nodePositions = new Map<string, {
            x: number; y: number;
            origX: number; origY: number;
            key: string;
            centroid: GroupCentroid;
        }>();
        const groupNodes = new Map<string, string[]>();
        const frameHeat = new Map<string, number>();

        // Initialize frame heat
        S.fixedGroupCentroids.forEach((_: GroupCentroid, key: string) => {
            frameHeat.set(key, 0);
        });

        // First pass: apply gravity and collect positions
        S.nodesDataset!.forEach((node: any) => {
            if (node.hidden || !positions[node.id]) return;

            const key = S.currentGroupKeyFn!(node.id);
            const centroid = S.fixedGroupCentroids!.get(key);
            if (!centroid) return;

            let nodeX = positions[node.id].x;
            let nodeY = positions[node.id].y;

            // Distance from group centroid
            const dx = nodeX - centroid.x;
            const dy = nodeY - centroid.y;
            const dist = Math.sqrt(dx * dx + dy * dy) || 1;

            // Strong gravity toward center - stronger for cross-group connections
            const crossCount = crossCounts.get(node.id) || 0;
            const gravityStrength = 0.05 + (crossCount / maxCross) * 0.4;

            if (dist > 5) {
                nodeX -= dx * gravityStrength;
                nodeY -= dy * gravityStrength;
            }

            nodePositions.set(node.id, {
                x: nodeX, y: nodeY,
                origX: positions[node.id].x, origY: positions[node.id].y,
                key: key, centroid: centroid,
            });

            if (!groupNodes.has(key)) groupNodes.set(key, []);
            groupNodes.get(key)!.push(node.id);
        });

        // Second pass: node-to-node repulsion + track proximity heat
        groupNodes.forEach((nodeIds, key) => {
            let proximityHeat = 0;
            for (let i = 0; i < nodeIds.length; i++) {
                for (let j = i + 1; j < nodeIds.length; j++) {
                    const posA = nodePositions.get(nodeIds[i])!;
                    const posB = nodePositions.get(nodeIds[j])!;

                    const dx = posB.x - posA.x;
                    const dy = posB.y - posA.y;
                    const dist = Math.sqrt(dx * dx + dy * dy) || 1;

                    if (dist < minNodeDist) {
                        const overlap = minNodeDist - dist;
                        const pushX = (dx / dist) * overlap * 0.3;
                        const pushY = (dy / dist) * overlap * 0.3;

                        posA.x -= pushX;
                        posA.y -= pushY;
                        posB.x += pushX;
                        posB.y += pushY;

                        proximityHeat += (overlap / minNodeDist) * 5;
                    }
                }
            }
            frameHeat.set(key, frameHeat.get(key)! + proximityHeat);
        });

        // Third pass: enforce boundaries, track boundary heat
        const updates: { id: string; x: number; y: number }[] = [];

        nodePositions.forEach((pos, nodeId) => {
            let { x: nodeX, y: nodeY, origX, origY, key, centroid } = pos;

            // Hard boundary - keep node inside its group circle
            const maxDist = centroid.radius - 40;
            const newDx = nodeX - centroid.x;
            const newDy = nodeY - centroid.y;
            const newDist = Math.sqrt(newDx * newDx + newDy * newDy);

            if (newDist > maxDist && maxDist > 0) {
                const overshoot = newDist - maxDist;
                frameHeat.set(key, frameHeat.get(key)! + overshoot * 0.5);

                const scale = maxDist / newDist;
                nodeX = centroid.x + newDx * scale;
                nodeY = centroid.y + newDy * scale;
            }

            // For _country nodes (have country but no IP data), push them OUT of IP sub-circles
            if (key.endsWith('|_country')) {
                const country = key.split('|')[0];
                S.fixedGroupCentroids!.forEach((subCircle: GroupCentroid, subKey: string) => {
                    if (!subKey.startsWith(country + '|') || subKey.endsWith('|_country')) return;
                    const sdx = nodeX - subCircle.x;
                    const sdy = nodeY - subCircle.y;
                    const sDist = Math.sqrt(sdx * sdx + sdy * sdy);
                    const minDist = subCircle.radius + 25;
                    if (sDist < minDist && sDist > 0) {
                        const pushScale = (minDist - sDist) / sDist;
                        nodeX += sdx * pushScale * 0.8;
                        nodeY += sdy * pushScale * 0.8;
                    }
                });
            }

            // Track velocity as heat
            const movement = Math.sqrt((nodeX - origX) ** 2 + (nodeY - origY) ** 2);
            frameHeat.set(key, frameHeat.get(key)! + movement * 0.2);

            if (Math.abs(nodeX - origX) > 0.3 || Math.abs(nodeY - origY) > 0.3) {
                updates.push({ id: nodeId, x: nodeX, y: nodeY });
            }
        });

        // Update heat with exponential moving average and adjust bubble sizes
        groupHeat.forEach((gh, key) => {
            const nodeCount = groupNodes.get(key)?.length || 1;
            const normalizedHeat = (frameHeat.get(key) || 0) / nodeCount;

            gh.heat = gh.heat * heatDecay + normalizedHeat * (1 - heatDecay);
            gh.nodeCount = nodeCount;

            const centroid = S.fixedGroupCentroids!.get(key);
            if (!centroid) return;

            if (gh.heat > heatThresholdHigh) {
                const expansion = Math.min((gh.heat - heatThresholdHigh) * 0.5, 10);
                centroid.radius += expansion;
            } else if (gh.heat < heatThresholdLow && centroid.radius > gh.baseRadius) {
                centroid.radius = Math.max(gh.baseRadius, centroid.radius - 1);
            }
        });

        if (updates.length > 0) {
            S.nodesDataset!.update(updates);
            updateGroupBoundaries();
        }
    }, 30));
}

export function stopGroupBoundaryEnforcement(): void {
    if (S.groupForceInterval) {
        clearInterval(S.groupForceInterval);
        S.setGroupForceInterval(null);
    }
    S.setCurrentGroupKeyFn(null);
    S.setFixedGroupCentroids(null);
}

// ---------------------------------------------------------------------------
// Boundary drawing & updating (exported)
// ---------------------------------------------------------------------------

/** Update group boundaries based on current node positions (circular bubbles) */
export function updateGroupBoundaries(): void {
    S.setGroupBoundaries([]);
    if (!S.network || !S.groupingMode) return;

    const positions = S.network.getPositions();
    const groups = new Map<string, {
        nodeIds: string[];
        color: { background: string; border: string };
        label: string;
        flag: string;
    }>();

    S.nodesDataset!.forEach((node: any) => {
        if (node.hidden) return;

        let groupKey: string;
        let color: { background: string; border: string };
        let label: string;
        let flag: string;

        if (S.groupingMode === 'country') {
            const svcInfo = S.visorServices[node.id];
            const country = svcInfo ? svcInfo.country : '';
            groupKey = country || '_unknown';
            color = getCountryColor(country);
            label = country || 'Unknown';
            flag = countryToFlag(country);
        } else if (S.groupingMode === 'ip' && S.ipGroupsData && S.ipGroupsData.groups) {
            const gid = S.ipGroupsData.groups[node.id];
            if (gid === undefined) {
                groupKey = '_no_ip';
                color = { background: 'rgba(102, 102, 102, 0.15)', border: '#666' };
                label = 'No IP Data';
                flag = '';
            } else {
                groupKey = 'ip_' + gid;
                color = {
                    background: getIPGroupColor(gid).replace(')', ', 0.15)').replace('hsl', 'hsla'),
                    border: getIPGroupColor(gid),
                };
                label = 'IP Group ' + gid;
                flag = '';
            }
        } else if (S.groupingMode === 'dual' && S.ipGroupsData && S.ipGroupsData.groups) {
            const svcInfo = S.visorServices[node.id];
            const country = svcInfo ? svcInfo.country : '';
            if (!country) return;
            const gid = S.ipGroupsData.groups[node.id];
            if (gid === undefined) return;

            groupKey = `${country}|${gid}`;
            color = {
                background: getIPGroupColor(gid).replace(')', ', 0.15)').replace('hsl', 'hsla'),
                border: getIPGroupColor(gid),
            };
            label = 'IP ' + gid;
            flag = '';
        } else {
            return;
        }

        if (!groups.has(groupKey)) {
            groups.set(groupKey, { nodeIds: [], color, label, flag });
        }
        groups.get(groupKey)!.nodeIds.push(node.id);
    });

    // Persist group-to-node mapping for cluster click selection
    S.groupNodeMap.clear();
    groups.forEach((group, key) => {
        S.groupNodeMap.set(key, new Set(group.nodeIds));
    });

    // Build circular boundaries for each group (inner boundaries)
    const boundaries: GroupBoundary[] = [];
    groups.forEach((group, key) => {
        const points = group.nodeIds
            .filter(id => positions[id])
            .map(id => ({ x: positions[id].x, y: positions[id].y }));

        if (points.length === 0) return;

        const cx = points.reduce((s, p) => s + p.x, 0) / points.length;
        const cy = points.reduce((s, p) => s + p.y, 0) / points.length;

        let maxDist = 0;
        for (const p of points) {
            const dist = Math.sqrt((p.x - cx) ** 2 + (p.y - cy) ** 2);
            if (dist > maxDist) maxDist = dist;
        }
        const radius = maxDist + 50;

        boundaries.push({
            centroid: { x: cx, y: cy },
            radius: Math.max(radius, 60),
            color: group.color,
            label: group.label,
            flag: group.flag,
            count: group.nodeIds.length,
            level: 'inner',
            groupKey: key,
            labelRect: null,
        });
    });

    // For dual mode: also add country-level outer boundaries
    if (S.groupingMode === 'dual') {
        const countryNodes = new Map<string, string[]>();
        S.nodesDataset!.forEach((node: any) => {
            if (node.hidden) return;
            const svcInfo = S.visorServices[node.id];
            const country = svcInfo ? svcInfo.country : '';
            if (!country) return;
            if (!countryNodes.has(country)) countryNodes.set(country, []);
            countryNodes.get(country)!.push(node.id);
        });

        countryNodes.forEach((nodeIds, country) => {
            const points = nodeIds
                .filter(id => positions[id])
                .map(id => ({ x: positions[id].x, y: positions[id].y }));

            if (points.length === 0) return;

            const cx = points.reduce((s, p) => s + p.x, 0) / points.length;
            const cy = points.reduce((s, p) => s + p.y, 0) / points.length;

            let maxDist = 0;
            for (const p of points) {
                const dist = Math.sqrt((p.x - cx) ** 2 + (p.y - cy) ** 2);
                if (dist > maxDist) maxDist = dist;
            }
            const radius = maxDist + 80;

            const countryColor = getCountryColor(country);

            const outerKey = '_outer_' + country;
            S.groupNodeMap.set(outerKey, new Set(nodeIds));
            boundaries.push({
                centroid: { x: cx, y: cy },
                radius: Math.max(radius, 100),
                color: { background: countryColor.background.replace('0.15', '0.08'), border: countryColor.border },
                label: country,
                flag: countryToFlag(country),
                count: nodeIds.length,
                level: 'outer',
                groupKey: outerKey,
                labelRect: null,
            });
        });
    }

    S.setGroupBoundaries(boundaries);
}

/** Draw circular group boundaries on canvas */
export function drawGroupBoundaries(ctx: CanvasRenderingContext2D): void {
    if (!S.groupBoundaries.length) return;

    // Draw satellite orbit rings first (behind everything)
    if (S.satelliteNodeIds.size > 0 && S.groupingMode === 'dual') {
        for (let lane = 0; lane < ORBIT_LANES; lane++) {
            const r = S.orbitBaseRadius + lane * ORBIT_LANE_SPACING;
            ctx.beginPath();
            ctx.arc(S.orbitCenter.x, S.orbitCenter.y, r, 0, 2 * Math.PI);
            ctx.strokeStyle = 'rgba(255, 255, 255, 0.05)';
            ctx.lineWidth = 1;
            ctx.setLineDash([4, 8]);
            ctx.stroke();
            ctx.setLineDash([]);
        }
    }

    // Draw outer boundaries first (behind inner ones) in dual mode
    const outerBoundaries = S.groupBoundaries.filter(b => b.level === 'outer');
    const innerBoundaries = S.groupBoundaries.filter(b => b.level !== 'outer');

    // Draw outer (country) boundaries with dashed stroke
    for (const boundary of outerBoundaries) {
        ctx.beginPath();
        ctx.arc(boundary.centroid.x, boundary.centroid.y, boundary.radius, 0, 2 * Math.PI);
        ctx.fillStyle = boundary.color.background;
        ctx.fill();
        ctx.strokeStyle = boundary.color.border;
        ctx.lineWidth = 2;
        ctx.setLineDash([8, 4]);
        ctx.stroke();
        ctx.setLineDash([]);

        // Country label at top
        ctx.font = 'bold 16px sans-serif';
        ctx.textAlign = 'center';
        ctx.textBaseline = 'middle';
        const labelText = (boundary.flag ? boundary.flag + ' ' : '') + boundary.label + ' (' + boundary.count + ')';
        const labelY = boundary.centroid.y - boundary.radius - 18;

        const metrics = ctx.measureText(labelText);
        const lx = boundary.centroid.x - metrics.width / 2 - 8;
        const ly = labelY - 14;
        const lw = metrics.width + 16;
        const lh = 28;
        boundary.labelRect = { x: lx, y: ly, w: lw, h: lh };
        ctx.fillStyle = 'rgba(0, 0, 0, 0.85)';
        ctx.fillRect(lx, ly, lw, lh);
        ctx.fillStyle = '#fff';
        ctx.fillText(labelText, boundary.centroid.x, labelY);
    }

    // Draw inner (IP group or single-mode) boundaries with solid stroke
    for (const boundary of innerBoundaries) {
        ctx.beginPath();
        ctx.arc(boundary.centroid.x, boundary.centroid.y, boundary.radius, 0, 2 * Math.PI);
        ctx.fillStyle = boundary.color.background;
        ctx.fill();
        ctx.strokeStyle = boundary.color.border;
        ctx.lineWidth = 3;
        ctx.stroke();

        // Label at top of circle
        ctx.font = 'bold 14px sans-serif';
        ctx.textAlign = 'center';
        ctx.textBaseline = 'middle';
        const labelText = (boundary.flag ? boundary.flag + ' ' : '') + boundary.label + ' (' + boundary.count + ')';
        const labelY = boundary.centroid.y - boundary.radius - 15;

        const metrics = ctx.measureText(labelText);
        const lx = boundary.centroid.x - metrics.width / 2 - 6;
        const ly = labelY - 12;
        const lw = metrics.width + 12;
        const lh = 24;
        boundary.labelRect = { x: lx, y: ly, w: lw, h: lh };
        ctx.fillStyle = 'rgba(0, 0, 0, 0.8)';
        ctx.fillRect(lx, ly, lw, lh);
        ctx.fillStyle = '#fff';
        ctx.fillText(labelText, boundary.centroid.x, labelY);
    }

    // Draw selection highlight ring around selected cluster
    if (S.selectedClusterId) {
        const selected = S.groupBoundaries.find(b => b.groupKey === S.selectedClusterId);
        if (selected) {
            ctx.beginPath();
            ctx.arc(selected.centroid.x, selected.centroid.y, selected.radius + 6, 0, 2 * Math.PI);
            ctx.strokeStyle = '#e94560';
            ctx.lineWidth = 4;
            ctx.setLineDash([12, 6]);
            ctx.stroke();
            ctx.setLineDash([]);
        }
    }
}

// ---------------------------------------------------------------------------
// Arrange nodes into groups (circle packing) -- exported
// ---------------------------------------------------------------------------

/** Arrange nodes into non-overlapping circular group clusters */
export function arrangeNodesIntoGroups(
    getGroupKey: (nodeId: string) => string,
    getGroupInfo: (key: string) => { color: { background: string; border: string }; label: string; flag: string },
    isRefresh?: boolean,
): void {
    if (!S.network) return;

    // Save original positions if not already saved
    if (!S.originalPositions) {
        S.setOriginalPositions(S.network.getPositions());
    }

    // Collect nodes by group
    const groups = new Map<string, string[]>();
    S.nodesDataset!.forEach((node: any) => {
        if (node.hidden) return;
        const key = getGroupKey(node.id);
        if (!groups.has(key)) {
            groups.set(key, []);
        }
        groups.get(key)!.push(node.id);
    });

    // Calculate radius for each group and sort by size (largest first)
    const groupsWithRadius = Array.from(groups.entries()).map(([key, nodeIds]) => ({
        key,
        nodeIds,
        radius: calculateGroupRadius(nodeIds.length),
    })).sort((a, b) => b.radius - a.radius);

    // Place circles using circle packing (no overlaps)
    const placedCircles: PlacedCircle[] = [];
    const padding = 60;
    const groupCentroids = new Map<string, GroupCentroid>();

    for (const group of groupsWithRadius) {
        const pos = findNonOverlappingPosition(group.radius, placedCircles, padding);
        placedCircles.push({ x: pos.x, y: pos.y, radius: group.radius });
        groupCentroids.set(group.key, {
            x: pos.x,
            y: pos.y,
            radius: group.radius,
            count: group.nodeIds.length,
        });
    }

    // Position nodes within their group's circle
    const nodeUpdates: { id: string; x: number; y: number }[] = [];
    for (const group of groupsWithRadius) {
        const centroid = groupCentroids.get(group.key)!;
        const nodeIds = group.nodeIds;
        const count = nodeIds.length;
        const innerRadius = centroid.radius - 50;

        nodeIds.forEach((nodeId, i) => {
            // On refresh, skip nodes that already existed (they keep their positions)
            if (isRefresh && !S.lastNewNodeIds.has(nodeId)) return;

            let x: number, y: number;
            if (count === 1) {
                x = centroid.x;
                y = centroid.y;
            } else if (count <= 6) {
                const angle = (i / count) * 2 * Math.PI;
                const r = innerRadius * 0.5;
                x = centroid.x + r * Math.cos(angle);
                y = centroid.y + r * Math.sin(angle);
            } else {
                const goldenAngle = Math.PI * (3 - Math.sqrt(5));
                const angle = i * goldenAngle;
                const r = innerRadius * Math.sqrt(i / count) * 0.9;
                x = centroid.x + r * Math.cos(angle);
                y = centroid.y + r * Math.sin(angle);
            }
            nodeUpdates.push({ id: nodeId, x: x, y: y });
        });
    }

    // Apply position updates
    if (nodeUpdates.length > 0) {
        S.nodesDataset!.update(nodeUpdates);
    }

    // Enable physics with extremely weak edge forces
    if (!S.userPhysicsDisabled) {
        S.network.setOptions({
            physics: {
                enabled: true,
                barnesHut: {
                    gravitationalConstant: -300,
                    springConstant: 0.00002,
                    springLength: 30,
                    damping: 0.98,
                },
                maxVelocity: 20,
                stabilization: false,
            },
        });
    }

    // Start group boundary enforcement
    startGroupBoundaryEnforcement(getGroupKey, groupCentroids);

    // Update boundaries and redraw
    setTimeout(() => {
        updateGroupBoundaries();
        if (!isRefresh) S.network!.fit();
        S.network!.redraw();
    }, 100);
}

// ---------------------------------------------------------------------------
// Grouping mode enablers / disablers (exported)
// ---------------------------------------------------------------------------

/** Enable country grouping */
export function enableCountryGrouping(isRefresh?: boolean): void {
    clearSatelliteNodes();
    S.setGroupingMode('country');

    arrangeNodesIntoGroups(
        (nodeId: string): string => {
            const svcInfo = S.visorServices[nodeId];
            return svcInfo ? (svcInfo.country || '_unknown') : '_unknown';
        },
        (key: string) => {
            const country = key === '_unknown' ? '' : key;
            return {
                color: getCountryColor(country),
                label: country || 'Unknown',
                flag: countryToFlag(country),
            };
        },
        isRefresh,
    );
}

/** Enable IP grouping */
export function enableIPGrouping(isRefresh?: boolean): void {
    if (!S.ipGroupsEnabled || !S.ipGroupsData) return;
    clearSatelliteNodes();
    S.setGroupingMode('ip');

    arrangeNodesIntoGroups(
        (nodeId: string): string => {
            const gid = S.ipGroupsData!.groups[nodeId];
            return gid !== undefined ? ('ip_' + gid) : '_no_ip';
        },
        (key: string) => {
            if (key === '_no_ip') {
                return {
                    color: { background: 'rgba(102, 102, 102, 0.15)', border: '#666' },
                    label: 'No IP Data',
                    flag: '',
                };
            }
            const gid = parseInt(key.replace('ip_', ''));
            return {
                color: {
                    background: getIPGroupColor(gid).replace(')', ', 0.15)').replace('hsl', 'hsla'),
                    border: getIPGroupColor(gid),
                },
                label: 'IP Group ' + gid,
                flag: '',
            };
        },
        isRefresh,
    );
}

/** Enable dual grouping: IP clusters nested within country clusters */
export function enableDualGrouping(isRefresh?: boolean): void {
    S.setGroupingMode('dual');
    if (!S.network) return;
    if (!S.originalPositions) S.setOriginalPositions(S.network.getPositions());

    // Build country -> ipGroup -> nodeIds hierarchy
    const hierarchy = new Map<string, Map<string, string[]>>();
    const unknownCountryNodes: string[] = [];
    S.nodesDataset!.forEach((node: any) => {
        if (node.hidden) return;
        const svcInfo = S.visorServices[node.id];
        const country = svcInfo ? svcInfo.country : '';
        const gid = S.ipGroupsData!.groups[node.id];
        const ipKey = gid !== undefined ? String(gid) : '_no_ip';

        if (!country) {
            unknownCountryNodes.push(node.id);
        } else {
            if (!hierarchy.has(country)) hierarchy.set(country, new Map());
            const ipMap = hierarchy.get(country)!;
            if (!ipMap.has(ipKey)) ipMap.set(ipKey, []);
            ipMap.get(ipKey)!.push(node.id);
        }
    });

    // Calculate country sizes and radii
    const countries: {
        country: string;
        totalNodes: number;
        radius: number;
        ipMap: Map<string, string[]>;
        actualIPGroups: number;
    }[] = [];

    hierarchy.forEach((ipMap, country) => {
        let totalNodes = 0;
        ipMap.forEach(nodes => totalNodes += nodes.length);
        const actualIPEntries = Array.from(ipMap.entries()).filter(([k]) => k !== '_no_ip');
        const actualIPGroups = actualIPEntries.length;

        let radius: number;
        if (actualIPGroups <= 1) {
            radius = calculateGroupRadius(totalNodes);
        } else {
            let totalSubCircleArea = 0;
            for (const [, nodeIds] of actualIPEntries) {
                const subRadius = calculateGroupRadius(nodeIds.length);
                // Use same padding as placeFibonacciSpiral (20) plus margin
                totalSubCircleArea += Math.PI * Math.pow(subRadius + 25, 2);
            }
            const noIpNodes = ipMap.get('_no_ip') || [];
            if (noIpNodes.length > 0) {
                totalSubCircleArea *= 1.15;
            }
            // Use lower packing efficiency (40%) to ensure circles fit without overlap
            const requiredRadius = Math.sqrt(totalSubCircleArea / (Math.PI * 0.40)) + 40;
            const maxSubRadius = Math.max(...actualIPEntries.map(([, ids]) => calculateGroupRadius(ids.length)));
            radius = Math.max(requiredRadius, maxSubRadius * 1.8);
        }
        countries.push({ country, totalNodes, radius, ipMap, actualIPGroups });
    });
    countries.sort((a, b) => b.radius - a.radius);

    // Place country circles
    const placedCountries: PlacedCircle[] = [];
    const countryCentroids = new Map<string, GroupCentroid>();

    for (const c of countries) {
        const pos = findNonOverlappingPosition(c.radius, placedCountries, 80);
        placedCountries.push({ x: pos.x, y: pos.y, radius: c.radius });
        countryCentroids.set(c.country, { x: pos.x, y: pos.y, radius: c.radius, count: c.totalNodes });
    }

    // Place IP sub-groups within each country
    const compositeGroups = new Map<string, GroupCentroid>();
    const nodeUpdates: { id: string; x: number; y: number }[] = [];

    for (const c of countries) {
        const cc = countryCentroids.get(c.country)!;

        const noIpNodes = c.ipMap.get('_no_ip') || [];
        const actualIPEntries = Array.from(c.ipMap.entries()).filter(([k]) => k !== '_no_ip');

        if (actualIPEntries.length === 0) {
            const key = `${c.country}|_country`;
            compositeGroups.set(key, { x: cc.x, y: cc.y, radius: cc.radius, count: noIpNodes.length });
            placeNodesInCircle(noIpNodes, cc, nodeUpdates);
        } else if (actualIPEntries.length === 1 && noIpNodes.length === 0) {
            const [ipKey, nodeIds] = actualIPEntries[0];
            const key = `${c.country}|${ipKey}`;
            compositeGroups.set(key, { x: cc.x, y: cc.y, radius: cc.radius, count: nodeIds.length });
            placeNodesInCircle(nodeIds, cc, nodeUpdates);
        } else {
            const ipGroups = actualIPEntries
                .map(([ipKey, nodeIds]) => ({
                    ipKey, nodeIds,
                    radius: calculateGroupRadius(nodeIds.length),
                }))
                .sort((a, b) => b.radius - a.radius);

            const placedSubs = placeFibonacciSpiral(ipGroups, cc);

            for (let i = 0; i < ipGroups.length; i++) {
                const ig = ipGroups[i];
                const pos = placedSubs[i];

                const key = `${c.country}|${ig.ipKey}`;
                compositeGroups.set(key, { x: pos.x, y: pos.y, radius: ig.radius, count: ig.nodeIds.length });
                placeNodesInCircle(ig.nodeIds, { x: pos.x, y: pos.y, radius: ig.radius }, nodeUpdates);
            }

            // Place _no_ip nodes freely inside country
            if (noIpNodes.length > 0) {
                const key = `${c.country}|_country`;
                compositeGroups.set(key, { x: cc.x, y: cc.y, radius: cc.radius, count: noIpNodes.length });
                placeNodesScattered(noIpNodes, cc, placedSubs, nodeUpdates);
            }
        }
    }

    // Place unknown country nodes as orbiting satellites
    if (unknownCountryNodes.length > 0) {
        calculateOrbitParams(placedCountries);
        initializeSatelliteOrbits(unknownCountryNodes);
        S.satelliteOrbits.forEach((orbit, id) => {
            const r = S.orbitBaseRadius + orbit.lane * ORBIT_LANE_SPACING;
            const x = S.orbitCenter.x + r * Math.cos(orbit.angle);
            const y = S.orbitCenter.y + r * Math.sin(orbit.angle);
            nodeUpdates.push({ id: id, x: x, y: y });
        });
    } else {
        clearSatelliteNodes();
    }

    // On refresh, only position genuinely new nodes
    const filteredUpdates = isRefresh
        ? nodeUpdates.filter(u => S.lastNewNodeIds.has(u.id))
        : nodeUpdates;
    if (filteredUpdates.length > 0) {
        S.nodesDataset!.update(filteredUpdates);
    }

    // Enable weak physics
    if (!S.userPhysicsDisabled) {
        S.network.setOptions({
            physics: {
                enabled: true,
                barnesHut: { gravitationalConstant: -300, springConstant: 0.00002, springLength: 30, damping: 0.98 },
                maxVelocity: 20,
                stabilization: false,
            },
        });
    }

    // Start boundary enforcement with composite keys
    startGroupBoundaryEnforcement(
        (nodeId: string): string => {
            const svcInfo = S.visorServices[nodeId];
            const country = svcInfo ? svcInfo.country : '';
            if (!country) return '_free';
            const gid = S.ipGroupsData!.groups[nodeId];
            if (gid === undefined) return `${country}|_country`;
            return `${country}|${gid}`;
        },
        compositeGroups,
    );

    // Store country centroids for outer boundary drawing
    S.setDualCountryCentroids(countryCentroids);

    setTimeout(() => {
        updateGroupBoundaries();
        if (S.satelliteNodeIds.size > 0) {
            applySatelliteStyles();
            startSatelliteOrbit();
        }
        if (!isRefresh) S.network!.fit();
        S.network!.redraw();
    }, 100);
}

/** Disable all grouping and restore original positions */
export function disableGrouping(isRefresh?: boolean): void {
    stopGroupBoundaryEnforcement();

    S.setGroupingMode(null);
    S.setGroupBoundaries([]);
    S.setDualCountryCentroids(null);

    // Clear cluster selection state
    S.groupNodeMap.clear();
    S.setSelectedClusterId(null);

    // Clear satellite orbits
    clearSatelliteNodes();

    if (S.network && S.originalPositions) {
        const updates: { id: string; x: number; y: number }[] = [];
        for (const [id, pos] of Object.entries(S.originalPositions)) {
            updates.push({ id: id, x: pos.x, y: pos.y });
        }
        S.nodesDataset!.update(updates);
        S.setOriginalPositions(null);

        // Re-enable normal physics
        if (!S.userPhysicsDisabled) {
            S.network.setOptions({
                physics: {
                    enabled: true,
                    barnesHut: {
                        gravitationalConstant: -3000,
                        springConstant: 0.001,
                        springLength: 200,
                    },
                },
            });
            S.network.stabilize(50);
            S.network.once('stabilizationIterationsDone', () => {
                if (!isRefresh) S.network!.fit();
            });
        }
    }

    if (S.network) S.network.redraw();
}

// ---------------------------------------------------------------------------
// Unified dispatcher (exported)
// ---------------------------------------------------------------------------

/** Unified grouping dispatcher based on checkbox state */
export function applyGrouping(isRefresh?: boolean): void {
    const countryChecked = (document.getElementById('cluster-country') as HTMLInputElement).checked;
    const ipChecked = (document.getElementById('cluster-ip') as HTMLInputElement).checked && S.ipGroupsEnabled && !!S.ipGroupsData;

    if (countryChecked && ipChecked) {
        enableDualGrouping(isRefresh);
    } else if (countryChecked) {
        enableCountryGrouping(isRefresh);
    } else if (ipChecked) {
        enableIPGrouping(isRefresh);
    } else {
        disableGrouping(isRefresh);
    }
}

// ---------------------------------------------------------------------------
// Setup (exported)
// ---------------------------------------------------------------------------

/** Setup drawing handler for group boundaries */
export function setupGroupDrawing(): void {
    if (!S.network) return;
    S.network.on('beforeDrawing', (ctx: CanvasRenderingContext2D) => {
        drawGroupBoundaries(ctx);
    });
    // Update boundaries when user drags nodes
    S.network.on('dragEnd', () => {
        if (S.groupingMode) {
            updateGroupBoundaries();
            S.network!.redraw();
        }
    });
}

// ---------------------------------------------------------------------------
// Hit-test cluster boundary for click detection
// ---------------------------------------------------------------------------

/** Hit-test cluster boundary for click detection */
export function hitTestClusterBoundary(params: any): GroupBoundary | null {
    if (!S.groupBoundaries.length || !S.network) return null;
    const canvasCoords = S.network.DOMtoCanvas(params.pointer.DOM);

    // Check inner boundaries first (more specific), then outer
    const sorted = [...S.groupBoundaries].sort((a, b) => {
        if (a.level === 'inner' && b.level === 'outer') return -1;
        if (a.level === 'outer' && b.level === 'inner') return 1;
        return a.radius - b.radius; // smaller first
    });

    for (const boundary of sorted) {
        // Check label rect hit
        if (boundary.labelRect) {
            const r = boundary.labelRect;
            if (canvasCoords.x >= r.x && canvasCoords.x <= r.x + r.w &&
                canvasCoords.y >= r.y && canvasCoords.y <= r.y + r.h) {
                return boundary;
            }
        }

        // Check border ring hit (15px tolerance around the stroke)
        const dx = canvasCoords.x - boundary.centroid.x;
        const dy = canvasCoords.y - boundary.centroid.y;
        const dist = Math.sqrt(dx * dx + dy * dy);
        if (Math.abs(dist - boundary.radius) <= 15) {
            return boundary;
        }
    }
    return null;
}
