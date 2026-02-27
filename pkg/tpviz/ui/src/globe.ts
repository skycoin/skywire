// Globe: 3D Earth visualization with nodes positioned by country
// Uses Three.js for WebGL rendering

import * as THREE from 'three';
import * as S from './state';
import { colors, LOCAL_VISOR_COLOR, LOCAL_EDGE_COLOR } from './constants';
import { getVisorStatus, countryToFlag, getIPGroupColor } from './utils';
import { showNodeInfo, hideNodeInfo } from './node-info';
import { CONTINENTS } from './world-data';
import { geoVoronoi } from 'd3-geo-voronoi';

// Country centroid coordinates (lat, lon) - approximate centers
const COUNTRY_COORDS: Record<string, [number, number]> = {
    'US': [37.0902, -95.7129],
    'CA': [56.1304, -106.3468],
    'MX': [23.6345, -102.5528],
    'BR': [-14.2350, -51.9253],
    'AR': [-38.4161, -63.6167],
    'CL': [-35.6751, -71.5430],
    'CO': [4.5709, -74.2973],
    'PE': [-9.1900, -75.0152],
    'VE': [6.4238, -66.5897],
    'GB': [55.3781, -3.4360],
    'DE': [51.1657, 10.4515],
    'FR': [46.2276, 2.2137],
    'ES': [40.4637, -3.7492],
    'IT': [41.8719, 12.5674],
    'PT': [39.3999, -8.2245],
    'NL': [52.1326, 5.2913],
    'BE': [50.5039, 4.4699],
    'CH': [46.8182, 8.2275],
    'AT': [47.5162, 14.5501],
    'PL': [51.9194, 19.1451],
    'CZ': [49.8175, 15.4730],
    'SE': [60.1282, 18.6435],
    'NO': [60.4720, 8.4689],
    'DK': [56.2639, 9.5018],
    'FI': [61.9241, 25.7482],
    'RU': [61.5240, 105.3188],
    'UA': [48.3794, 31.1656],
    'RO': [45.9432, 24.9668],
    'HU': [47.1625, 19.5033],
    'GR': [39.0742, 21.8243],
    'TR': [38.9637, 35.2433],
    'CN': [35.8617, 104.1954],
    'JP': [36.2048, 138.2529],
    'KR': [35.9078, 127.7669],
    'IN': [20.5937, 78.9629],
    'TH': [15.8700, 100.9925],
    'VN': [14.0583, 108.2772],
    'ID': [-0.7893, 113.9213],
    'MY': [4.2105, 101.9758],
    'SG': [1.3521, 103.8198],
    'PH': [12.8797, 121.7740],
    'AU': [-25.2744, 133.7751],
    'NZ': [-40.9006, 174.8860],
    'ZA': [-30.5595, 22.9375],
    'EG': [26.8206, 30.8025],
    'NG': [9.0820, 8.6753],
    'KE': [-0.0236, 37.9062],
    'IL': [31.0461, 34.8516],
    'AE': [23.4241, 53.8478],
    'SA': [23.8859, 45.0792],
    'IR': [32.4279, 53.6880],
    'PK': [30.3753, 69.3451],
    'BD': [23.6850, 90.3563],
    'HK': [22.3193, 114.1694],
    'TW': [23.6978, 120.9605],
    'IE': [53.4129, -8.2439],
    'SK': [48.6690, 19.6990],
    'BG': [42.7339, 25.4858],
    'HR': [45.1000, 15.2000],
    'SI': [46.1512, 14.9955],
    'RS': [44.0165, 21.0059],
    'LT': [55.1694, 23.8813],
    'LV': [56.8796, 24.6032],
    'EE': [58.5953, 25.0136],
    'BY': [53.7098, 27.9534],
    'MD': [47.4116, 28.3699],
    'AL': [41.1533, 20.1683],
    'MK': [41.5124, 21.7453],
    'BA': [43.9159, 17.6791],
    'ME': [42.7087, 19.3744],
    'CY': [35.1264, 33.4299],
    'LU': [49.8153, 6.1296],
    'MT': [35.9375, 14.3754],
    'IS': [64.9631, -19.0208],
};

// Country colors for Voronoi regions
const COUNTRY_COLORS: Record<string, string> = {
    'US': '#1e90ff', 'CA': '#dc143c', 'MX': '#006400', 'BR': '#ffd700', 'AR': '#87ceeb',
    'GB': '#4169e1', 'DE': '#000000', 'FR': '#00008b', 'ES': '#ff4500', 'IT': '#228b22',
    'NL': '#ff8c00', 'BE': '#8b0000', 'CH': '#ff0000', 'AT': '#dc143c', 'PL': '#ffffff',
    'RU': '#0000cd', 'UA': '#ffd700', 'CN': '#ff0000', 'JP': '#ff69b4', 'KR': '#4169e1',
    'IN': '#ff8c00', 'AU': '#006400', 'ZA': '#228b22', 'SE': '#ffd700', 'NO': '#ff0000',
};

// Globe state
let scene: THREE.Scene | null = null;
let camera: THREE.PerspectiveCamera | null = null;
let renderer: THREE.WebGLRenderer | null = null;
let globe: THREE.Mesh | null = null;
let atmosphere: THREE.Mesh | null = null;
let nodeGroup: THREE.Group | null = null;
let edgeGroup: THREE.Group | null = null;
let voronoiGroup: THREE.Group | null = null;
let interiorLinesGroup: THREE.Group | null = null;
let animationId: number | null = null;
let isGlobeActive = false;

// Voronoi mode state
let voronoiMode = true; // Default to Voronoi mode
let showVoronoiOverlay = true; // Toggle for Voronoi region overlay visibility
let nodePositions: Map<string, THREE.Vector3> = new Map();

// Interaction state
let isDragging = false;
let previousMousePosition = { x: 0, y: 0 };
let autoRotate = true;
let rotationSpeed = 0.001;

// Raycaster for node selection
const raycaster = new THREE.Raycaster();
const mouse = new THREE.Vector2();

// Node sprites for hover/selection
const nodeSprites: Map<string, THREE.Sprite> = new Map();
let hoveredNode: string | null = null;
let selectedGlobeNode: string | null = null;

// Generate evenly distributed points on sphere using Fibonacci spiral
function fibonacciSphere(numPoints: number, radius: number): THREE.Vector3[] {
    const points: THREE.Vector3[] = [];
    const goldenRatio = (1 + Math.sqrt(5)) / 2;

    for (let i = 0; i < numPoints; i++) {
        const theta = 2 * Math.PI * i / goldenRatio;
        const phi = Math.acos(1 - 2 * (i + 0.5) / numPoints);

        const x = radius * Math.sin(phi) * Math.cos(theta);
        const y = radius * Math.sin(phi) * Math.sin(theta);
        const z = radius * Math.cos(phi);

        points.push(new THREE.Vector3(x, y, z));
    }

    return points;
}

// Generate points distributed around a center point on sphere surface
function distributePointsAroundCenter(
    center: THREE.Vector3,
    numPoints: number,
    maxAngle: number,  // Maximum angular distance from center (radians)
    radius: number
): THREE.Vector3[] {
    if (numPoints === 0) return [];
    if (numPoints === 1) return [center.clone().normalize().multiplyScalar(radius)];

    const points: THREE.Vector3[] = [];
    const goldenRatio = (1 + Math.sqrt(5)) / 2;

    // Create local coordinate system at center
    const centerNorm = center.clone().normalize();
    let tangent1 = new THREE.Vector3(1, 0, 0);
    if (Math.abs(centerNorm.dot(tangent1)) > 0.9) {
        tangent1.set(0, 1, 0);
    }
    tangent1 = new THREE.Vector3().crossVectors(centerNorm, tangent1).normalize();
    const tangent2 = new THREE.Vector3().crossVectors(centerNorm, tangent1).normalize();

    for (let i = 0; i < numPoints; i++) {
        // Use Fibonacci-like distribution within a cone
        const theta = 2 * Math.PI * i / goldenRatio;
        // Distribute radially with sqrt for uniform density
        const r = maxAngle * Math.sqrt((i + 0.5) / numPoints);

        // Convert polar coords in tangent plane to 3D point
        const offset = tangent1.clone().multiplyScalar(Math.sin(r) * Math.cos(theta))
            .add(tangent2.clone().multiplyScalar(Math.sin(r) * Math.sin(theta)))
            .add(centerNorm.clone().multiplyScalar(Math.cos(r)));

        points.push(offset.normalize().multiplyScalar(radius));
    }

    return points;
}

// Find closest country centroid to a point (for assigning unknown locations)
function findClosestCountry(point: THREE.Vector3, countryCentroids: Map<string, THREE.Vector3>): string {
    let closest = '';
    let minDist = Infinity;

    for (const [country, centroid] of countryCentroids) {
        const dist = point.distanceTo(centroid);
        if (dist < minDist) {
            minDist = dist;
            closest = country;
        }
    }

    return closest;
}

// Create a great circle arc on sphere surface
function createGreatCircleArc(
    start: THREE.Vector3,
    end: THREE.Vector3,
    radius: number,
    segments: number = 32
): THREE.Vector3[] {
    const points: THREE.Vector3[] = [];

    for (let i = 0; i <= segments; i++) {
        const t = i / segments;
        // Spherical linear interpolation
        const point = new THREE.Vector3();
        const angle = start.angleTo(end);

        if (angle < 0.001) {
            point.copy(start);
        } else {
            const sinAngle = Math.sin(angle);
            const a = Math.sin((1 - t) * angle) / sinAngle;
            const b = Math.sin(t * angle) / sinAngle;
            point.copy(start).multiplyScalar(a).add(end.clone().multiplyScalar(b));
        }

        point.normalize().multiplyScalar(radius);
        points.push(point);
    }

    return points;
}

// Calculate spherical Delaunay triangulation using convex hull
function calculateSphericalDelaunay(points: THREE.Vector3[]): number[][] {
    if (points.length < 4) return [];

    // Use convex hull of 3D points - each face is a Delaunay triangle on the sphere
    const triangles: number[][] = [];

    // Simple convex hull algorithm for sphere points
    // Since all points are on sphere surface, convex hull faces = Delaunay triangles
    const n = points.length;

    // For each triplet, check if it forms a valid triangle (all other points on one side)
    for (let i = 0; i < n - 2; i++) {
        for (let j = i + 1; j < n - 1; j++) {
            for (let k = j + 1; k < n; k++) {
                const p1 = points[i];
                const p2 = points[j];
                const p3 = points[k];

                // Calculate face normal
                const v1 = new THREE.Vector3().subVectors(p2, p1);
                const v2 = new THREE.Vector3().subVectors(p3, p1);
                const normal = new THREE.Vector3().crossVectors(v1, v2).normalize();

                // Check if normal points outward (toward center or away)
                const center = new THREE.Vector3().add(p1).add(p2).add(p3).divideScalar(3);
                if (normal.dot(center) < 0) {
                    normal.negate();
                }

                // Check all other points are on the same side
                let valid = true;
                let side = 0;
                for (let m = 0; m < n && valid; m++) {
                    if (m === i || m === j || m === k) continue;
                    const d = new THREE.Vector3().subVectors(points[m], p1).dot(normal);
                    if (side === 0) {
                        side = d > 0 ? 1 : -1;
                    } else if ((d > 0.001 && side < 0) || (d < -0.001 && side > 0)) {
                        valid = false;
                    }
                }

                if (valid) {
                    triangles.push([i, j, k]);
                }
            }
        }
    }

    return triangles;
}

// Calculate Voronoi cell boundaries on sphere
function calculateSphericalVoronoi(
    points: THREE.Vector3[],
    triangles: number[][]
): Map<number, THREE.Vector3[]> {
    const voronoiCells = new Map<number, THREE.Vector3[]>();

    // Initialize empty cells for each point
    for (let i = 0; i < points.length; i++) {
        voronoiCells.set(i, []);
    }

    // For each triangle, calculate circumcenter (Voronoi vertex)
    const circumcenters: THREE.Vector3[] = [];
    const triangleToCircumcenter = new Map<string, number>();

    for (let t = 0; t < triangles.length; t++) {
        const [i, j, k] = triangles[t];
        const p1 = points[i];
        const p2 = points[j];
        const p3 = points[k];

        // Circumcenter on sphere is the normalized cross product direction
        const v1 = new THREE.Vector3().subVectors(p2, p1);
        const v2 = new THREE.Vector3().subVectors(p3, p1);
        const circumcenter = new THREE.Vector3().crossVectors(v1, v2).normalize();

        // Make sure it points outward
        const center = new THREE.Vector3().add(p1).add(p2).add(p3).divideScalar(3);
        if (circumcenter.dot(center) < 0) {
            circumcenter.negate();
        }

        circumcenters.push(circumcenter);
        triangleToCircumcenter.set(`${i}-${j}-${k}`, t);

        // Add circumcenter to each point's Voronoi cell
        voronoiCells.get(i)!.push(circumcenter.clone());
        voronoiCells.get(j)!.push(circumcenter.clone());
        voronoiCells.get(k)!.push(circumcenter.clone());
    }

    // Sort vertices in each cell by angle around the point
    for (const [idx, vertices] of voronoiCells) {
        if (vertices.length < 3) continue;

        const point = points[idx];

        // Create a local coordinate system on the tangent plane
        const up = point.clone().normalize();
        const right = new THREE.Vector3(1, 0, 0);
        if (Math.abs(up.dot(right)) > 0.9) {
            right.set(0, 1, 0);
        }
        const tangent1 = new THREE.Vector3().crossVectors(up, right).normalize();
        const tangent2 = new THREE.Vector3().crossVectors(up, tangent1).normalize();

        // Calculate angle for each vertex
        vertices.sort((a, b) => {
            const da = new THREE.Vector3().subVectors(a, point);
            const db = new THREE.Vector3().subVectors(b, point);
            const angleA = Math.atan2(da.dot(tangent2), da.dot(tangent1));
            const angleB = Math.atan2(db.dot(tangent2), db.dot(tangent1));
            return angleA - angleB;
        });
    }

    return voronoiCells;
}

// Create interior line (chord through sphere)
function createInteriorLine(
    start: THREE.Vector3,
    end: THREE.Vector3,
    color: string,
    opacity: number = 0.6
): THREE.Line {
    const geometry = new THREE.BufferGeometry().setFromPoints([start, end]);
    const material = new THREE.LineBasicMaterial({
        color: color,
        transparent: true,
        opacity: opacity,
        linewidth: 2,
    });
    return new THREE.Line(geometry, material);
}

// Convert lat/lon to 3D coordinates on sphere
function latLonToVector3(lat: number, lon: number, radius: number): THREE.Vector3 {
    const phi = (90 - lat) * (Math.PI / 180);
    const theta = (lon + 180) * (Math.PI / 180);

    const x = -(radius * Math.sin(phi) * Math.cos(theta));
    const z = radius * Math.sin(phi) * Math.sin(theta);
    const y = radius * Math.cos(phi);

    return new THREE.Vector3(x, y, z);
}

// Create geodesic curve between two points on sphere
function createGeodesicCurve(
    start: THREE.Vector3,
    end: THREE.Vector3,
    radius: number,
    segments: number = 64
): THREE.CurvePath<THREE.Vector3> {
    const curve = new THREE.CurvePath<THREE.Vector3>();

    // Calculate the angle between the two points
    const angle = start.angleTo(end);

    // If points are very close, just use a straight line
    if (angle < 0.01) {
        curve.add(new THREE.LineCurve3(start, end));
        return curve;
    }

    // Calculate the arc height based on distance
    const arcHeight = Math.min(0.3, angle * 0.2);

    // Create points along the great circle arc
    const points: THREE.Vector3[] = [];
    for (let i = 0; i <= segments; i++) {
        const t = i / segments;

        // Spherical interpolation (slerp)
        const point = new THREE.Vector3().copy(start).lerp(end, t);
        point.normalize();

        // Add arc height - higher in the middle
        const heightFactor = Math.sin(t * Math.PI);
        const height = radius + arcHeight * heightFactor;
        point.multiplyScalar(height);

        points.push(point);
    }

    // Create curve from points
    for (let i = 0; i < points.length - 1; i++) {
        curve.add(new THREE.LineCurve3(points[i], points[i + 1]));
    }

    return curve;
}

// Create Earth texture with country outlines using accurate continental data
function createEarthTexture(): THREE.Texture {
    const canvas = document.createElement('canvas');
    canvas.width = 2048;
    canvas.height = 1024;
    const ctx = canvas.getContext('2d')!;

    // Dark background (ocean)
    ctx.fillStyle = '#0a1628';
    ctx.fillRect(0, 0, canvas.width, canvas.height);

    // Draw latitude/longitude grid
    ctx.strokeStyle = 'rgba(50, 80, 120, 0.3)';
    ctx.lineWidth = 1;

    // Longitude lines
    for (let lon = -180; lon <= 180; lon += 30) {
        const x = ((lon + 180) / 360) * canvas.width;
        ctx.beginPath();
        ctx.moveTo(x, 0);
        ctx.lineTo(x, canvas.height);
        ctx.stroke();
    }

    // Latitude lines
    for (let lat = -90; lat <= 90; lat += 30) {
        const y = ((90 - lat) / 180) * canvas.height;
        ctx.beginPath();
        ctx.moveTo(0, y);
        ctx.lineTo(canvas.width, y);
        ctx.stroke();
    }

    // Helper to convert lon/lat to canvas coords
    const toCanvas = (lon: number, lat: number): [number, number] => {
        const x = ((lon + 180) / 360) * canvas.width;
        const y = ((90 - lat) / 180) * canvas.height;
        return [x, y];
    };

    // Draw a polygon path
    const drawPath = (points: [number, number][]) => {
        if (points.length < 3) return;
        ctx.beginPath();
        let [x, y] = toCanvas(points[0][0], points[0][1]);
        ctx.moveTo(x, y);
        for (let i = 1; i < points.length; i++) {
            [x, y] = toCanvas(points[i][0], points[i][1]);
            ctx.lineTo(x, y);
        }
        ctx.closePath();
        ctx.fill();
        ctx.stroke();
    };

    // Draw all continents from world data
    ctx.strokeStyle = 'rgba(0, 217, 165, 0.6)';
    ctx.fillStyle = 'rgba(0, 100, 80, 0.15)';
    ctx.lineWidth = 2;

    for (const continent of CONTINENTS) {
        for (const path of continent.paths) {
            drawPath(path);
        }
    }

    const texture = new THREE.CanvasTexture(canvas);
    texture.needsUpdate = true;
    return texture;
}

// Initialize the globe scene
export function initGlobe(): void {
    const container = document.getElementById('globe-container');
    if (!container) return;

    // Create scene
    scene = new THREE.Scene();
    scene.background = new THREE.Color(0x1a1a2e);

    // Create camera
    const aspect = container.clientWidth / container.clientHeight;
    camera = new THREE.PerspectiveCamera(45, aspect, 0.1, 1000);
    camera.position.z = 2.5; // Closer camera = larger globe relative to nodes

    // Create renderer
    renderer = new THREE.WebGLRenderer({ antialias: true });
    renderer.setSize(container.clientWidth, container.clientHeight);
    renderer.setPixelRatio(window.devicePixelRatio);
    container.appendChild(renderer.domElement);

    // Create globe
    const geometry = new THREE.SphereGeometry(1, 64, 64);
    const texture = createEarthTexture();
    const material = new THREE.MeshPhongMaterial({
        map: texture,
        transparent: true,
        opacity: voronoiMode ? 0.15 : 1.0,
        shininess: 5,
    });
    globe = new THREE.Mesh(geometry, material);
    scene.add(globe);

    // Create atmosphere glow
    const atmosphereGeometry = new THREE.SphereGeometry(1.05, 64, 64);
    const atmosphereMaterial = new THREE.ShaderMaterial({
        vertexShader: `
            varying vec3 vNormal;
            void main() {
                vNormal = normalize(normalMatrix * normal);
                gl_Position = projectionMatrix * modelViewMatrix * vec4(position, 1.0);
            }
        `,
        fragmentShader: `
            varying vec3 vNormal;
            void main() {
                float intensity = pow(0.7 - dot(vNormal, vec3(0.0, 0.0, 1.0)), 2.0);
                gl_FragColor = vec4(0.0, 0.6, 0.8, 1.0) * intensity;
            }
        `,
        blending: THREE.AdditiveBlending,
        side: THREE.BackSide,
        transparent: true,
    });
    atmosphere = new THREE.Mesh(atmosphereGeometry, atmosphereMaterial);
    atmosphere.visible = !voronoiMode;
    scene.add(atmosphere);

    // Create groups for nodes and edges
    nodeGroup = new THREE.Group();
    edgeGroup = new THREE.Group();
    voronoiGroup = new THREE.Group();
    interiorLinesGroup = new THREE.Group();
    scene.add(voronoiGroup);
    scene.add(interiorLinesGroup);
    scene.add(nodeGroup);
    scene.add(edgeGroup);

    // Add lights
    const ambientLight = new THREE.AmbientLight(0x404040, 2);
    scene.add(ambientLight);

    const directionalLight = new THREE.DirectionalLight(0xffffff, 1);
    directionalLight.position.set(5, 3, 5);
    scene.add(directionalLight);

    // Event listeners
    container.addEventListener('mousedown', onMouseDown);
    container.addEventListener('mousemove', onMouseMove);
    container.addEventListener('mouseup', onMouseUp);
    container.addEventListener('wheel', onWheel);
    container.addEventListener('click', onGlobeClick);
    window.addEventListener('resize', onWindowResize);

    // Start animation
    animate();
}

// Animation loop
function animate(): void {
    if (!isGlobeActive) return;

    animationId = requestAnimationFrame(animate);

    if (globe && autoRotate && !isDragging) {
        globe.rotation.y += rotationSpeed;
        if (nodeGroup) nodeGroup.rotation.y += rotationSpeed;
        if (edgeGroup) edgeGroup.rotation.y += rotationSpeed;
        if (voronoiGroup) voronoiGroup.rotation.y += rotationSpeed;
        if (interiorLinesGroup) interiorLinesGroup.rotation.y += rotationSpeed;
    }

    if (renderer && scene && camera) {
        renderer.render(scene, camera);
    }
}

// Mouse event handlers
function onMouseDown(event: MouseEvent): void {
    isDragging = true;
    autoRotate = false;
    previousMousePosition = { x: event.clientX, y: event.clientY };
}

function onMouseMove(event: MouseEvent): void {
    if (!isDragging || !globe || !nodeGroup || !edgeGroup) return;

    const deltaX = event.clientX - previousMousePosition.x;
    const deltaY = event.clientY - previousMousePosition.y;

    // Free rotation in all directions (no clamping)
    globe.rotation.y += deltaX * 0.005;
    globe.rotation.x += deltaY * 0.005;

    nodeGroup.rotation.y = globe.rotation.y;
    nodeGroup.rotation.x = globe.rotation.x;
    edgeGroup.rotation.y = globe.rotation.y;
    edgeGroup.rotation.x = globe.rotation.x;
    if (voronoiGroup) {
        voronoiGroup.rotation.y = globe.rotation.y;
        voronoiGroup.rotation.x = globe.rotation.x;
    }
    if (interiorLinesGroup) {
        interiorLinesGroup.rotation.y = globe.rotation.y;
        interiorLinesGroup.rotation.x = globe.rotation.x;
    }

    previousMousePosition = { x: event.clientX, y: event.clientY };
}

function onMouseUp(): void {
    isDragging = false;
    // Resume auto-rotation after 3 seconds of inactivity
    setTimeout(() => {
        if (!isDragging) autoRotate = true;
    }, 3000);
}

function onWheel(event: WheelEvent): void {
    if (!camera) return;

    event.preventDefault();

    const delta = event.deltaY > 0 ? 0.1 : -0.1;
    camera.position.z = Math.max(1.5, Math.min(10, camera.position.z + delta));
}

function onWindowResize(): void {
    const container = document.getElementById('globe-container');
    if (!container || !camera || !renderer) return;

    camera.aspect = container.clientWidth / container.clientHeight;
    camera.updateProjectionMatrix();
    renderer.setSize(container.clientWidth, container.clientHeight);
}

function onGlobeClick(event: MouseEvent): void {
    if (!camera || !nodeGroup) return;

    const container = document.getElementById('globe-container');
    if (!container) return;

    const rect = container.getBoundingClientRect();
    mouse.x = ((event.clientX - rect.left) / container.clientWidth) * 2 - 1;
    mouse.y = -((event.clientY - rect.top) / container.clientHeight) * 2 + 1;

    raycaster.setFromCamera(mouse, camera);

    const intersects = raycaster.intersectObjects(nodeGroup.children, true);

    if (intersects.length > 0) {
        const sprite = intersects[0].object as THREE.Sprite;
        const nodeId = sprite.userData.nodeId;
        if (nodeId) {
            selectedGlobeNode = nodeId;
            showNodeInfo(nodeId);
        }
    } else {
        selectedGlobeNode = null;
        hideNodeInfo();
    }
}

// Get country coordinates with jitter for multiple nodes in same country
function getCountryPosition(country: string, index: number = 0): [number, number] {
    const coords = COUNTRY_COORDS[country] || [0, 0];

    // Add jitter for multiple nodes in same country
    const jitterLat = (Math.random() - 0.5) * 5;
    const jitterLon = (Math.random() - 0.5) * 8;

    return [coords[0] + jitterLat, coords[1] + jitterLon];
}

// Update globe with current network data
export function updateGlobeData(): void {
    if (!nodeGroup || !edgeGroup || !S.nodesDataset || !S.edgesDataset) return;

    // Clear existing nodes and edges
    while (nodeGroup.children.length > 0) {
        nodeGroup.remove(nodeGroup.children[0]);
    }
    while (edgeGroup.children.length > 0) {
        edgeGroup.remove(edgeGroup.children[0]);
    }
    if (voronoiGroup) {
        while (voronoiGroup.children.length > 0) {
            voronoiGroup.remove(voronoiGroup.children[0]);
        }
    }
    if (interiorLinesGroup) {
        while (interiorLinesGroup.children.length > 0) {
            interiorLinesGroup.remove(interiorLinesGroup.children[0]);
        }
    }
    nodeSprites.clear();
    nodePositions.clear();

    // Get all nodes and calculate positions for ALL of them first
    const nodes = S.nodesDataset.get();
    const countryNodeCounts: Record<string, number> = {};

    if (voronoiMode && nodes.length >= 3) {
        // VORONOI MODE: Even Fibonacci distribution with geographic country clustering

        // Step 1: Generate Fibonacci positions for ALL nodes (even distribution)
        const fibPositions = fibonacciSphere(nodes.length, 1.02);

        // Step 2: Group nodes by country, then by IP group within each country
        const nodesByCountry: Map<string, Map<number | string, any[]>> = new Map();
        nodes.forEach((node: any) => {
            const country = node.country || S.visorServices[node.id]?.country || '';
            const ipGroup = S.ipGroupsData?.groups?.[node.id] ?? '_no_ip';

            if (!nodesByCountry.has(country)) {
                nodesByCountry.set(country, new Map());
            }
            const countryMap = nodesByCountry.get(country)!;
            if (!countryMap.has(ipGroup)) {
                countryMap.set(ipGroup, []);
            }
            countryMap.get(ipGroup)!.push(node);
        });

        // Step 3: Calculate country centroids and total node counts
        interface CountryData {
            country: string;
            centroid: THREE.Vector3;
            nodeCount: number;
            ipGroups: Map<number | string, any[]>;
        }
        const countryDataList: CountryData[] = [];

        for (const [country, ipMap] of nodesByCountry) {
            const coords = COUNTRY_COORDS[country];
            const centroid = coords
                ? latLonToVector3(coords[0], coords[1], 1.02)
                : latLonToVector3(0, 0, 1.02); // Default to 0,0 for unknown

            let nodeCount = 0;
            ipMap.forEach(nodes => nodeCount += nodes.length);

            countryDataList.push({ country, centroid, nodeCount, ipGroups: ipMap });
        }

        // Step 4: For each country, find the Fibonacci position closest to its centroid
        // Then assign a contiguous block of Fibonacci positions to that country
        interface CountryAssignment {
            country: string;
            bestFibIndex: number;
            nodeCount: number;
            ipGroups: Map<number | string, any[]>;
        }
        const countryAssignments: CountryAssignment[] = countryDataList.map(cd => {
            // Find Fibonacci position closest to country centroid
            let bestIndex = 0;
            let bestDist = Infinity;
            for (let i = 0; i < fibPositions.length; i++) {
                const dist = fibPositions[i].distanceTo(cd.centroid);
                if (dist < bestDist) {
                    bestDist = dist;
                    bestIndex = i;
                }
            }
            return {
                country: cd.country,
                bestFibIndex: bestIndex,
                nodeCount: cd.nodeCount,
                ipGroups: cd.ipGroups
            };
        });

        // Sort countries by their best Fibonacci index (spiral order)
        countryAssignments.sort((a, b) => a.bestFibIndex - b.bestFibIndex);

        // Step 5: Assign contiguous Fibonacci positions to each country's nodes
        // Countries get positions in spiral order, maintaining geographic clustering
        let positionIndex = 0;
        const nodeToCountry: Map<string, string> = new Map();
        const nodeToIPGroup: Map<string, number | string> = new Map();

        interface CountryIPGroup {
            country: string;
            ipGroup: number | string;
            nodes: any[];
        }
        const sortedGroups: CountryIPGroup[] = [];

        for (const ca of countryAssignments) {
            // Sort IP groups within this country
            const sortedIPGroups = Array.from(ca.ipGroups.entries()).sort((a, b) => {
                if (a[0] === '_no_ip') return 1;
                if (b[0] === '_no_ip') return -1;
                return (a[0] as number) - (b[0] as number);
            });

            for (const [ipGroup, groupNodes] of sortedIPGroups) {
                sortedGroups.push({ country: ca.country, ipGroup, nodes: groupNodes });

                for (const node of groupNodes) {
                    if (positionIndex < fibPositions.length) {
                        nodePositions.set(node.id, fibPositions[positionIndex]);
                        nodeToCountry.set(node.id, ca.country);
                        nodeToIPGroup.set(node.id, ipGroup);
                        positionIndex++;
                    }
                }
            }
        }

        // Step 5: Draw Voronoi BOUNDARIES ONLY (no filled cells)
        // Thick lines between countries, thin lines between IP groups within same country
        if (voronoiGroup && showVoronoiOverlay && sortedGroups.length >= 3) {
            // Calculate centroid for each IP group
            const groupCentroids: { country: string; ipGroup: number | string; centroid: THREE.Vector3; lon: number; lat: number }[] = [];

            for (const { country, ipGroup, nodes: groupNodes } of sortedGroups) {
                let sumX = 0, sumY = 0, sumZ = 0;
                let count = 0;
                for (const node of groupNodes) {
                    const pos = nodePositions.get(node.id);
                    if (pos) {
                        sumX += pos.x;
                        sumY += pos.y;
                        sumZ += pos.z;
                        count++;
                    }
                }
                if (count > 0) {
                    const centroid = new THREE.Vector3(sumX / count, sumY / count, sumZ / count);
                    centroid.normalize().multiplyScalar(1.02);
                    const r = centroid.length();
                    const lat = 90 - Math.acos(centroid.y / r) * (180 / Math.PI);
                    const lon = Math.atan2(centroid.z, -centroid.x) * (180 / Math.PI);
                    groupCentroids.push({ country, ipGroup, centroid, lon, lat });
                }
            }

            // Convert to geo points for d3-geo-voronoi
            const geoPoints: [number, number][] = groupCentroids.map(g => [g.lon, g.lat]);

            if (geoPoints.length >= 3) {
                try {
                    const voronoi = geoVoronoi(geoPoints);
                    const polygons = voronoi.polygons();

                    if (polygons && polygons.features) {
                        // Track which edges we've drawn to avoid duplicates
                        const drawnEdges = new Set<string>();

                        // For each cell, look at its edges and determine if it's a country or IP boundary
                        for (let i = 0; i < polygons.features.length; i++) {
                            const feature = polygons.features[i];
                            const groupData = groupCentroids[i];
                            if (!feature || !feature.geometry || !groupData) continue;

                            const coords = feature.geometry.coordinates;
                            if (!coords || coords.length === 0) continue;

                            const ring = coords[0];
                            if (!ring || ring.length < 3) continue;

                            // For each edge of this cell's polygon
                            for (let j = 0; j < ring.length; j++) {
                                const pt1 = ring[j];
                                const pt2 = ring[(j + 1) % ring.length];

                                // Create edge key to avoid drawing twice
                                const edgeKey = [
                                    `${pt1[0].toFixed(4)},${pt1[1].toFixed(4)}`,
                                    `${pt2[0].toFixed(4)},${pt2[1].toFixed(4)}`
                                ].sort().join('|');

                                if (drawnEdges.has(edgeKey)) continue;
                                drawnEdges.add(edgeKey);

                                // Find which other cell shares this edge (neighbor)
                                let neighborCountry: string | null = null;
                                for (let k = 0; k < polygons.features.length; k++) {
                                    if (k === i) continue;
                                    const otherRing = polygons.features[k]?.geometry?.coordinates?.[0];
                                    if (!otherRing) continue;

                                    // Check if this edge exists in the other polygon
                                    for (let m = 0; m < otherRing.length; m++) {
                                        const oPt1 = otherRing[m];
                                        const oPt2 = otherRing[(m + 1) % otherRing.length];
                                        const otherEdgeKey = [
                                            `${oPt1[0].toFixed(4)},${oPt1[1].toFixed(4)}`,
                                            `${oPt2[0].toFixed(4)},${oPt2[1].toFixed(4)}`
                                        ].sort().join('|');

                                        if (otherEdgeKey === edgeKey) {
                                            neighborCountry = groupCentroids[k]?.country || null;
                                            break;
                                        }
                                    }
                                    if (neighborCountry !== null) break;
                                }

                                // Determine line style based on whether it's a country or IP boundary
                                const isCountryBoundary = neighborCountry !== null && neighborCountry !== groupData.country;

                                // Draw the edge as a great circle arc
                                const arcPoints = createGreatCircleArc(
                                    latLonToVector3(pt1[1], pt1[0], 1.015),
                                    latLonToVector3(pt2[1], pt2[0], 1.015),
                                    1.015,
                                    16
                                );

                                const edgeGeometry = new THREE.BufferGeometry().setFromPoints(arcPoints);
                                const edgeMaterial = new THREE.LineBasicMaterial({
                                    color: isCountryBoundary ? 0xffffff : 0x00d9a5,
                                    transparent: true,
                                    opacity: isCountryBoundary ? 0.9 : 0.5,
                                    linewidth: isCountryBoundary ? 3 : 1,
                                });
                                const edgeLine = new THREE.Line(edgeGeometry, edgeMaterial);
                                voronoiGroup.add(edgeLine);
                            }
                        }
                    }
                } catch (e) {
                    console.error('Voronoi calculation failed:', e);
                }
            }
        }
    } else {
        // GEOGRAPHIC MODE: Position by country
        nodes.forEach((node: any) => {
            const country = node.country || S.visorServices[node.id]?.country || '';
            if (!countryNodeCounts[country]) countryNodeCounts[country] = 0;
            const [lat, lon] = getCountryPosition(country, countryNodeCounts[country]++);
            const position = latLonToVector3(lat, lon, 1.02);
            nodePositions.set(node.id, position);
        });
    }

    // Now filter for visible nodes when rendering sprites
    const showOnline = (document.getElementById('show-online') as HTMLInputElement)?.checked ?? true;
    const showOffline = (document.getElementById('show-offline') as HTMLInputElement)?.checked ?? false;
    const showUnknown = (document.getElementById('show-unknown') as HTMLInputElement)?.checked ?? false;

    // Create node sprites (filtered by visibility)
    nodes.forEach((node: any) => {
        const country = node.country || S.visorServices[node.id]?.country || '';
        const status = getVisorStatus(node.id);

        // Filter based on status
        if (status === 'online' && !showOnline) return;
        if (status === 'offline' && !showOffline) return;
        if (status === 'unknown' && !showUnknown) return;

        const position = nodePositions.get(node.id);
        if (!position) return;

        // Create sprite
        const canvas = document.createElement('canvas');
        canvas.width = 64;
        canvas.height = 64;
        const ctx = canvas.getContext('2d')!;

        // Determine color
        let fillColor = '#ffd166'; // unknown
        if (status === 'online') fillColor = '#00d9a5';
        else if (status === 'offline') fillColor = '#e94560';
        if (node.isLocal) fillColor = LOCAL_VISOR_COLOR.background;

        // Draw node
        ctx.beginPath();
        ctx.arc(32, 32, 20, 0, Math.PI * 2);
        ctx.fillStyle = fillColor;
        ctx.fill();

        if (node.isLocal) {
            ctx.strokeStyle = '#ff00ff';
            ctx.lineWidth = 4;
            ctx.stroke();
        }

        // Draw flag if available
        const flag = countryToFlag(country);
        if (flag) {
            ctx.font = '20px Arial';
            ctx.textAlign = 'center';
            ctx.textBaseline = 'middle';
            ctx.fillText(flag, 32, 32);
        }

        const texture = new THREE.CanvasTexture(canvas);
        const spriteMaterial = new THREE.SpriteMaterial({
            map: texture,
            transparent: true,
        });
        const sprite = new THREE.Sprite(spriteMaterial);
        sprite.position.copy(position);
        sprite.scale.set(0.045, 0.045, 1); // Smaller nodes relative to globe
        sprite.userData.nodeId = node.id;
        sprite.userData.country = country;

        nodeGroup!.add(sprite);
        nodeSprites.set(node.id, sprite);
    });

    // Create edges
    const edges = S.edgesDataset.get();
    edges.forEach((edge: any) => {
        const fromPos = nodePositions.get(edge.from);
        const toPos = nodePositions.get(edge.to);

        if (!fromPos || !toPos) return;

        // Check edge type filter
        const showSTCPR = (document.getElementById('show-stcpr') as HTMLInputElement)?.checked;
        const showSUDPH = (document.getElementById('show-sudph') as HTMLInputElement)?.checked;
        const showDMSG = (document.getElementById('show-dmsg') as HTMLInputElement)?.checked;
        const showDMSGServers = (document.getElementById('show-dmsg-servers') as HTMLInputElement)?.checked;
        const showRoutes = (document.getElementById('show-routes') as HTMLInputElement)?.checked;

        if (edge.type === 'stcpr' && !showSTCPR) return;
        if (edge.type === 'sudph' && !showSUDPH) return;
        if (edge.type === 'dmsg' && !showDMSG) return;
        if (edge.type === 'dmsg-connection' && !showDMSGServers) return;
        if (edge.type === 'route' && !showRoutes) return;

        // Determine color - check edge's own color first, then fallback to type-based colors
        let color: string;
        if (edge.color && typeof edge.color === 'object' && edge.color.color) {
            color = edge.color.color;
        } else if (edge.color && typeof edge.color === 'string') {
            color = edge.color;
        } else {
            color = colors[edge.type] || '#888888';  // Gray fallback for unknown types
        }
        if (edge.isLocal || edge.isLocalOnly) color = LOCAL_EDGE_COLOR;

        if (voronoiMode && interiorLinesGroup) {
            // VORONOI MODE: Draw interior chord (straight line through sphere)
            const line = createInteriorLine(fromPos, toPos, color, edge.isLocal ? 1 : 0.6);
            interiorLinesGroup.add(line);
        } else {
            // GEOGRAPHIC MODE: Draw geodesic curve on surface
            const curve = createGeodesicCurve(fromPos, toPos, 1);
            const points = curve.getPoints(50);
            const geometry = new THREE.BufferGeometry().setFromPoints(points);

            const material = new THREE.LineBasicMaterial({
                color: color,
                opacity: edge.isLocal ? 1 : 0.5,
                transparent: true,
                linewidth: edge.isLocal ? 2 : 1,
            });

            const line = new THREE.Line(geometry, material);
            edgeGroup!.add(line);
        }
    });
}

// Show the globe view
export function showGlobe(): void {
    isGlobeActive = true;

    const networkEl = document.getElementById('network');
    const globeContainer = document.getElementById('globe-container');

    if (networkEl) networkEl.style.display = 'none';
    if (globeContainer) globeContainer.style.display = 'block';

    if (!scene) {
        initGlobe();
    }

    updateGlobeData();
    animate();
}

// Hide the globe view
export function hideGlobe(): void {
    isGlobeActive = false;

    const networkEl = document.getElementById('network');
    const globeContainer = document.getElementById('globe-container');

    if (networkEl) networkEl.style.display = 'block';
    if (globeContainer) globeContainer.style.display = 'none';

    if (animationId) {
        cancelAnimationFrame(animationId);
        animationId = null;
    }
}

// Toggle between globe and 2D views
export function toggleGlobeView(): void {
    if (isGlobeActive) {
        hideGlobe();
    } else {
        showGlobe();
    }
}

// Check if globe is currently active
export function isGlobeViewActive(): boolean {
    return isGlobeActive;
}

// Toggle Voronoi mode
export function setVoronoiMode(enabled: boolean): void {
    voronoiMode = enabled;

    // Update globe transparency
    if (globe) {
        const material = globe.material as THREE.MeshPhongMaterial;
        material.opacity = enabled ? 0.15 : 1.0;
    }

    // Update atmosphere visibility
    if (atmosphere) {
        atmosphere.visible = !enabled;
    }

    if (isGlobeActive) {
        updateGlobeData();
    }
}

// Check if Voronoi mode is active
export function isVoronoiModeActive(): boolean {
    return voronoiMode;
}

// Toggle Voronoi overlay visibility (show/hide region coloring)
export function setVoronoiOverlay(visible: boolean): void {
    showVoronoiOverlay = visible;

    // Update voronoiGroup visibility
    if (voronoiGroup) {
        voronoiGroup.visible = visible;
    }

    // No need to recalculate - just toggle visibility
    if (renderer && scene && camera) {
        renderer.render(scene, camera);
    }
}

// Check if Voronoi overlay is visible
export function isVoronoiOverlayVisible(): boolean {
    return showVoronoiOverlay;
}

// Dispose of globe resources
export function disposeGlobe(): void {
    if (animationId) {
        cancelAnimationFrame(animationId);
    }

    if (renderer) {
        renderer.dispose();
        const container = document.getElementById('globe-container');
        if (container && renderer.domElement.parentNode === container) {
            container.removeChild(renderer.domElement);
        }
    }

    scene = null;
    camera = null;
    renderer = null;
    globe = null;
    nodeGroup = null;
    edgeGroup = null;
    voronoiGroup = null;
    interiorLinesGroup = null;
    nodePositions.clear();
    isGlobeActive = false;
}
