//go:build js && wasm

package cosmos

import (
	"fmt"
	"strings"
)

// All shaders are carried over verbatim from cosmos.gl 2.6.3.

const quadVert = `#ifdef GL_ES
precision highp float;
#endif

attribute vec2 vertexCoord; // Vertex coordinates in normalized device coordinates
varying vec2 textureCoords; // Texture coordinates to pass to the fragment shader

void main() {
    // Convert vertex coordinates from [-1, 1] range to [0, 1] range for texture sampling
    textureCoords = (vertexCoord + 1.0) / 2.0;
    gl_Position = vec4(vertexCoord, 0, 1);
}`

const clearFrag = `#ifdef GL_ES
precision highp float;
#endif

void main() {
  gl_FragColor = vec4(0.0);
}`

const updatePositionFrag = `#ifdef GL_ES
precision highp float;
#endif

uniform sampler2D positionsTexture;
uniform sampler2D velocity;
uniform sampler2D pinnedStatusTexture;
uniform float friction;
uniform float spaceSize;

varying vec2 textureCoords;

void main() {
  vec4 pointPosition = texture2D(positionsTexture, textureCoords);
  vec4 pointVelocity = texture2D(velocity, textureCoords);

  // Check if point is pinned
  // pinnedStatusTexture has the same size and layout as positionsTexture
  // Each pixel corresponds to a point: red channel > 0.5 means the point is pinned
  vec4 pinnedStatus = texture2D(pinnedStatusTexture, textureCoords);

  // If pinned, don't update position
  if (pinnedStatus.r > 0.5) {
    gl_FragColor = pointPosition;
    return;
  }

  // Friction
  pointVelocity.rg *= friction;

  pointPosition.rg += pointVelocity.rg;

  pointPosition.r = clamp(pointPosition.r, 0.0, spaceSize);
  pointPosition.g = clamp(pointPosition.g, 0.0, spaceSize);

  gl_FragColor = pointPosition;
}`

const dragPointFrag = `#ifdef GL_ES
precision highp float;
#endif

uniform sampler2D positionsTexture;
uniform vec2 mousePos;
uniform float index;

varying vec2 textureCoords;

void main() {
  vec4 pointPosition = texture2D(positionsTexture, textureCoords);

  // Check if a point is being dragged
  if (index >= 0.0 && index == pointPosition.b) {
    pointPosition.rg = mousePos.rg;
  }

  gl_FragColor = pointPosition;
}`

const drawPointsVert = `#ifdef GL_ES
precision highp float;
#endif

attribute vec2 pointIndices;
attribute float size;
attribute vec4 color;
attribute float shape;
attribute float imageIndex;
attribute float imageSize;

uniform sampler2D positionsTexture;
uniform sampler2D pointGreyoutStatus;
uniform sampler2D imageAtlasCoords;
uniform float ratio;
uniform mat3 transformationMatrix;
uniform float pointsTextureSize;
uniform float sizeScale;
uniform float spaceSize;
uniform vec2 screenSize;

uniform vec4 greyoutColor;
uniform vec4 backgroundColor;
uniform bool scalePointsOnZoom;
uniform float maxPointSize;
uniform bool isDarkenGreyout;
uniform bool skipSelected;
uniform bool skipUnselected;
uniform bool hasImages;
uniform float imageCount;
uniform float imageAtlasCoordsTextureSize;

varying float pointShape;
varying float isGreyedOut;
varying vec4 shapeColor;
varying vec4 imageAtlasUV;
varying float shapeSize;
varying float imageSizeVarying;
varying float overallSize;

float calculatePointSize(float size) {
  float pSize;
  if (scalePointsOnZoom) {
    pSize = size * ratio * transformationMatrix[0][0];
  } else {
    pSize = size * ratio * min(5.0, max(1.0, transformationMatrix[0][0] * 0.01));
  }

  return min(pSize, maxPointSize * ratio);
}

void main() {
  // Check greyout status for selective rendering
  vec4 greyoutStatus = texture2D(pointGreyoutStatus, (pointIndices + 0.5) / pointsTextureSize);
  isGreyedOut = greyoutStatus.r;
  bool isSelected = greyoutStatus.r == 0.0;

  // Discard point based on rendering mode
  if (skipSelected && isSelected) {
    gl_Position = vec4(2.0, 2.0, 2.0, 1.0); // Move off-screen
    gl_PointSize = 0.0;
    return;
  }
  if (skipUnselected && !isSelected) {
    gl_Position = vec4(2.0, 2.0, 2.0, 1.0); // Move off-screen
    gl_PointSize = 0.0;
    return;
  }

  // Position
  vec4 pointPosition = texture2D(positionsTexture, (pointIndices + 0.5) / pointsTextureSize);
  vec2 point = pointPosition.rg;

  // Transform point position to normalized device coordinates
  vec2 normalizedPosition = 2.0 * point / spaceSize - 1.0;
  normalizedPosition *= spaceSize / screenSize;
  vec3 finalPosition = transformationMatrix * vec3(normalizedPosition, 1);
  gl_Position = vec4(finalPosition.rg, 0, 1);

  // Calculate sizes for shape and image
  float shapeSizeValue = calculatePointSize(size * sizeScale);
  float imageSizeValue = calculatePointSize(imageSize * sizeScale);

  // Use the larger of the two sizes for the overall point size
  float overallSizeValue = max(shapeSizeValue, imageSizeValue);
  gl_PointSize = overallSizeValue;

  // Pass size information to fragment shader
  shapeSize = shapeSizeValue;
  imageSizeVarying = imageSizeValue;
  overallSize = overallSizeValue;

  shapeColor = color;
  pointShape = shape;

  // Adjust alpha of selected points
  if (isGreyedOut > 0.0) {
    if (greyoutColor[0] != -1.0) {
      shapeColor = greyoutColor;
    } else {
      // If greyoutColor is not set, make color lighter or darker based on isDarkenGreyout
      float blendFactor = 0.65; // Controls how much to modify (0.0 = original, 1.0 = target color)

      if (isDarkenGreyout) {
        // Darken the color
        shapeColor.rgb = mix(shapeColor.rgb, vec3(0.2), blendFactor);
      } else {
        // Lighten the color
        shapeColor.rgb = mix(shapeColor.rgb, max(backgroundColor.rgb, vec3(0.8)), blendFactor);
      }
    }
  }

  if (!hasImages || imageIndex < 0.0 || imageIndex >= imageCount) {
    imageAtlasUV = vec4(-1.0);
    return;
  }
  // Calculate image atlas UV coordinates based on imageIndex
  float atlasCoordIndex = imageIndex;
  // Calculate the position in the texture grid
  float texX = mod(atlasCoordIndex, imageAtlasCoordsTextureSize);
  float texY = floor(atlasCoordIndex / imageAtlasCoordsTextureSize);
  // Convert to texture coordinates (0.0 to 1.0)
  vec2 atlasCoordTexCoord = (vec2(texX, texY) + 0.5) / imageAtlasCoordsTextureSize;
  vec4 atlasCoords = texture2D(imageAtlasCoords, atlasCoordTexCoord);
  imageAtlasUV = atlasCoords;
}`

const drawPointsFrag = `// Fragment shader for rendering points with various shapes and smooth edges

#ifdef GL_ES
precision highp float;
#endif

uniform float greyoutOpacity;
uniform float pointOpacity;
uniform sampler2D imageAtlasTexture;
uniform bool isDarkenGreyout;
uniform vec4 backgroundColor;


varying float pointShape;
varying float isGreyedOut;
varying vec4 shapeColor;
varying vec4 imageAtlasUV;
varying float shapeSize;
varying float imageSizeVarying;
varying float overallSize;

// Smoothing controls the smoothness of the point's edge
const float smoothing = 0.9;

// Shape constants
const float CIRCLE = 0.0;
const float SQUARE = 1.0;
const float TRIANGLE = 2.0;
const float DIAMOND = 3.0;
const float PENTAGON = 4.0;
const float HEXAGON = 5.0;
const float STAR = 6.0;
const float CROSS = 7.0;
const float NONE = 8.0;

// Distance functions for different shapes
float circleDistance(vec2 p) {
    return dot(p, p);
}

// Function to apply greyout logic to image colors
vec4 applyGreyoutToImage(vec4 imageColor) {
    vec3 finalColor = imageColor.rgb;
    float finalAlpha = imageColor.a;

    if (isGreyedOut > 0.0) {
        float blendFactor = 0.65; // Controls how much to modify (0.0 = original, 1.0 = target color)

        if (isDarkenGreyout) {
            finalColor = mix(finalColor, vec3(0.2), blendFactor);
        } else {
            finalColor = mix(finalColor, max(backgroundColor.rgb, vec3(0.8)), blendFactor);
        }
    }

    return vec4(finalColor, finalAlpha);
}

float squareDistance(vec2 p) {
    vec2 d = abs(p) - vec2(0.8);
    return length(max(d, 0.0)) + min(max(d.x, d.y), 0.0);
}

float triangleDistance(vec2 p) {
    const float k = sqrt(3.0);   // ≈1.732; slope of 60° lines for an equilateral triangle
    p.x = abs(p.x) - 0.9;        // fold the X axis and shift: brings left and right halves together
    p.y = p.y + 0.55;             // move the whole shape up slightly so it is centred vertically

    // reflect points that fall outside the main triangle back inside, to reuse the same maths
    if (p.x + k * p.y > 0.0)
        p = vec2(p.x - k * p.y,  -k * p.x - p.y) / 2.0;

    p.x -= clamp(p.x, -1.0, 0.0); // clip any remainder on the left side

    // Return signed distance: negative = inside; positive = outside
    return -length(p) * sign(p.y);
}

float diamondDistance(vec2 p) {
    // aspect > 1  →  taller diamond
    const float aspect = 1.2;
    return abs(p.x) + abs(p.y) / aspect - 0.8;
}

float pentagonDistance(vec2 p) {
    // Regular pentagon signed-distance (Inigo Quilez)
    const vec3 k = vec3(0.809016994, 0.587785252, 0.726542528);
    p.x = abs(p.x);

    // Reflect across the two tilted edges ─ only if point is outside
    p -= 2.0 * min(dot(vec2(-k.x, k.y), p), 0.0) * vec2(-k.x, k.y);
    p -= 2.0 * min(dot(vec2( k.x, k.y), p), 0.0) * vec2( k.x, k.y);

    // Clip against the top horizontal edge (keeps top point sharp)
    p -= vec2(clamp(p.x, -k.z * k.x, k.z * k.x), k.z);

    // Return signed distance (negative → inside, positive → outside)
    return length(p) * sign(p.y);
}

float hexagonDistance(vec2 p) {
    const vec3 k = vec3(-0.866025404, 0.5, 0.577350269);
    p = abs(p);
    p -= 2.0 * min(dot(k.xy, p), 0.0) * k.xy;
    p -= vec2(clamp(p.x, -k.z * 0.8, k.z * 0.8), 0.8);
    return length(p) * sign(p.y);
}

float starDistance(vec2 p) {
    // 5-point star signed-distance function (adapted from Inigo Quilez)
    // r  – outer radius, rf – inner/outer radius ratio
    const float r  = 0.9;
    const float rf = 0.45;

    // Pre-computed rotation vectors for the star arms (36° increments)
    const vec2 k1 = vec2(0.809016994, -0.587785252);
    const vec2 k2 = vec2(-k1.x, k1.y);

    // Fold the plane into a single arm sector
    p.x = abs(p.x);
    p -= 2.0 * max(dot(k1, p), 0.0) * k1;
    p -= 2.0 * max(dot(k2, p), 0.0) * k2;
    p.x = abs(p.x);

    // Translate so the top tip of the star lies on the X-axis
    p.y -= r;

    // Vector describing the edge between an outer tip and its adjacent inner point
    vec2 ba = rf * vec2(-k1.y, k1.x) - vec2(0.0, 1.0);
    // Project the point onto that edge and clamp the projection to the segment
    float h = clamp(dot(p, ba) / dot(ba, ba), 0.0, r);

    // Return signed distance (negative => inside, positive => outside)
    return length(p - ba * h) * sign(p.y * ba.x - p.x * ba.y);
}

float crossDistance(vec2 p) {
    // Signed distance function for a cross (union of two rectangles)
    // Adapted from Inigo Quilez (https://iquilezles.org/)
    // Each arm has half-sizes 0.3 (thickness) and 0.8 (length)
    p = abs(p);
    if (p.y > p.x) p = p.yx;       // exploit symmetry

    vec2 q = p - vec2(0.8, 0.3);   // subtract half-sizes (length, thickness)

    // Standard rectangle SDF, then take union of the two arms
    return length(max(q, 0.0)) + min(max(q.x, q.y), 0.0);
}

float getShapeDistance(vec2 p, float shape) {
    if (shape == SQUARE) return squareDistance(p);
    else if (shape == TRIANGLE) return triangleDistance(p);
    else if (shape == DIAMOND) return diamondDistance(p);
    else if (shape == PENTAGON) return pentagonDistance(p);
    else if (shape == HEXAGON) return hexagonDistance(p);
    else if (shape == STAR) return starDistance(p);
    else if (shape == CROSS) return crossDistance(p);
    else return circleDistance(p); // Default to circle
}

void main() {
    // Discard the fragment if the point is fully transparent and has no image
    if (shapeColor.a == 0.0 && imageAtlasUV.x == -1.0) {
        discard;
    }

    // Discard the fragment if the point has no shape and no image
    if (pointShape == NONE && imageAtlasUV.x == -1.0) {
        discard;
    }

    // Calculate coordinates within the point
    vec2 pointCoord = 2.0 * gl_PointCoord - 1.0;

    vec4 finalShapeColor = vec4(0.0);
    vec4 finalImageColor = vec4(0.0);

    // Handle shape rendering with centering logic
    if (pointShape != NONE) {
        // Calculate shape coordinates with centering
        vec2 shapeCoord = pointCoord;
        if (overallSize > shapeSize && shapeSize > 0.0) {
            // Shape is smaller than overall size, center it
            float scale = shapeSize / overallSize;
            shapeCoord = pointCoord / scale;
        }

        float opacity;
        if (pointShape == CIRCLE) {
            // For circles, use the original distance calculation
            float pointCenterDistance = dot(shapeCoord, shapeCoord);
            opacity = 1.0 - smoothstep(smoothing, 1.0, pointCenterDistance);
        } else {
            // For other shapes, use the shape distance function
            float shapeDistance = getShapeDistance(shapeCoord, pointShape);
            opacity = 1.0 - smoothstep(-0.01, 0.01, shapeDistance);
        }
        opacity *= shapeColor.a;

        finalShapeColor = vec4(shapeColor.rgb, opacity);
    }

    // Handle image rendering with centering logic
    if (imageAtlasUV.x != -1.0) {
        // Calculate image coordinates with centering
        vec2 imageCoord = pointCoord;
        if (overallSize > imageSizeVarying && imageSizeVarying > 0.0) {
            // Image is smaller than overall size, center it
            float scale = imageSizeVarying / overallSize;
            imageCoord = pointCoord / scale;

            // Check if we're outside the valid image area
            if (abs(imageCoord.x) > 1.0 || abs(imageCoord.y) > 1.0) {
                // We're outside the image bounds, don't render the image
                finalImageColor = vec4(0.0);
            } else {
                // Sample from texture atlas
                vec2 atlasUV = mix(imageAtlasUV.xy, imageAtlasUV.zw, (imageCoord + 1.0) * 0.5);
                vec4 imageColor = texture2D(imageAtlasTexture, atlasUV);
                finalImageColor = applyGreyoutToImage(imageColor);
            }
        } else {
            // Image is same size or larger than overall size, no scaling needed
            // Sample from texture atlas
            vec2 atlasUV = mix(imageAtlasUV.xy, imageAtlasUV.zw, (imageCoord + 1.0) * 0.5);
            vec4 imageColor = texture2D(imageAtlasTexture, atlasUV);
            finalImageColor = applyGreyoutToImage(imageColor);
        }
    }

    float finalPointAlpha = max(finalShapeColor.a, finalImageColor.a);
    if (isGreyedOut > 0.0 && greyoutOpacity != -1.0) {
        finalPointAlpha *= greyoutOpacity;
    } else {
        finalPointAlpha *= pointOpacity;
    }

    // Blend image color above point color
    gl_FragColor = vec4(
        mix(finalShapeColor.rgb, finalImageColor.rgb, finalImageColor.a),
        finalPointAlpha
    );
}`

const drawHighlightedVert = `precision mediump float;

attribute vec2 vertexCoord;

uniform sampler2D positionsTexture;
uniform sampler2D pointGreyoutStatusTexture;
uniform float size;
uniform mat3 transformationMatrix;
uniform float pointsTextureSize;
uniform float sizeScale;
uniform float spaceSize;
uniform vec2 screenSize;
uniform bool scalePointsOnZoom;
uniform float pointIndex;
uniform float maxPointSize;
uniform vec4 color;
uniform float universalPointOpacity;
uniform float greyoutOpacity;
uniform bool isDarkenGreyout;
uniform vec4 backgroundColor;
uniform vec4 greyoutColor;
varying vec2 vertexPosition;
varying float pointOpacity;
varying vec3 rgbColor;

float calculatePointSize(float size) {
  float pSize;
  if (scalePointsOnZoom) {
    pSize = size * transformationMatrix[0][0];
  } else {
    pSize = size * min(5.0, max(1.0, transformationMatrix[0][0] * 0.01));
  }
  return min(pSize, maxPointSize);
}

const float relativeRingRadius = 1.3;

void main () {
  vertexPosition = vertexCoord;

  vec2 textureCoordinates = vec2(mod(pointIndex, pointsTextureSize), floor(pointIndex / pointsTextureSize)) + 0.5;
  vec4 pointPosition = texture2D(positionsTexture, textureCoordinates / pointsTextureSize);

  rgbColor = color.rgb;
  pointOpacity = color.a * universalPointOpacity;
  vec4 greyoutStatus = texture2D(pointGreyoutStatusTexture, textureCoordinates / pointsTextureSize);
  if (greyoutStatus.r > 0.0) {
    if (greyoutColor[0] != -1.0) {
      rgbColor = greyoutColor.rgb;
      pointOpacity = greyoutColor.a;
    } else {
      // If greyoutColor is not set, make color lighter or darker based on isDarkenGreyout
      float blendFactor = 0.65; // Controls how much to modify (0.0 = original, 1.0 = target color)

      if (isDarkenGreyout) {
        // Darken the color
        rgbColor = mix(rgbColor, vec3(0.2), blendFactor);
      } else {
        // Lighten the color
        rgbColor = mix(rgbColor, max(backgroundColor.rgb, vec3(0.8)), blendFactor);
      }
    }

    if (greyoutOpacity != -1.0) {
      pointOpacity *= greyoutOpacity;
    }
  }

  // Calculate point radius
  float pointSize = (calculatePointSize(size * sizeScale) * relativeRingRadius) / transformationMatrix[0][0];
  float radius = pointSize * 0.5;

  // Calculate point position in screen space
  vec2 a = pointPosition.xy;
  vec2 b = pointPosition.xy + vec2(0.0, radius);
  vec2 xBasis = b - a;
  vec2 yBasis = normalize(vec2(-xBasis.y, xBasis.x));
  vec2 pointPositionInScreenSpace = a + xBasis * vertexCoord.x + yBasis * radius * vertexCoord.y;

  // Transform point position to normalized device coordinates
  vec2 p = 2.0 * pointPositionInScreenSpace / spaceSize - 1.0;
  p *= spaceSize / screenSize;
  vec3 final =  transformationMatrix * vec3(p, 1);

  gl_Position = vec4(final.rg, 0, 1);
}`

const drawHighlightedFrag = `precision mediump float;

uniform float width;

varying vec2 vertexPosition;
varying float pointOpacity;
varying vec3 rgbColor;

const float smoothing = 1.05;

void main () {
  float r = dot(vertexPosition, vertexPosition);
  float opacity = smoothstep(r, r * smoothing, 1.0);
  float stroke = smoothstep(width, width * smoothing, r);
  gl_FragColor = vec4(rgbColor, opacity * stroke * pointOpacity);
}`

const findPointsOnAreaSelectionFrag = `#ifdef GL_ES
precision highp float;
#endif

uniform sampler2D positionsTexture;
uniform sampler2D pointSize;
uniform float sizeScale;
uniform float spaceSize;
uniform vec2 screenSize;
uniform float ratio;
uniform mat3 transformationMatrix;
uniform vec2 selection0;
uniform vec2 selection1;
uniform bool scalePointsOnZoom;
uniform float maxPointSize;

varying vec2 textureCoords;

float pointSizeF(float size) {
  float pSize;
  if (scalePointsOnZoom) {
    pSize = size * ratio * transformationMatrix[0][0];
  } else {
    pSize = size * ratio * min(5.0, max(1.0, transformationMatrix[0][0] * 0.01));
  }
  return min(pSize, maxPointSize * ratio);
}

void main() {
  vec4 pointPosition = texture2D(positionsTexture, textureCoords);
  vec2 p = 2.0 * pointPosition.rg / spaceSize - 1.0;
  p *= spaceSize / screenSize;
  vec3 final = transformationMatrix * vec3(p, 1);

  vec4 pSize = texture2D(pointSize, textureCoords);
  float size = pSize.r * sizeScale;

  float left = 2.0 * (selection0.x - 0.5 * pointSizeF(size)) / screenSize.x - 1.0;
  float right = 2.0 * (selection1.x + 0.5 * pointSizeF(size)) / screenSize.x - 1.0;
  float top =  2.0 * (selection0.y - 0.5 * pointSizeF(size)) / screenSize.y - 1.0;
  float bottom =  2.0 * (selection1.y + 0.5 * pointSizeF(size)) / screenSize.y - 1.0;

  gl_FragColor = vec4(0.0, 0.0, pointPosition.rg);
  if (final.x >= left && final.x <= right && final.y >= top && final.y <= bottom) {
    gl_FragColor.r = 1.0;
  }
}`

const findPointsOnPolygonSelectionFrag = `#ifdef GL_ES
precision highp float;
#endif

uniform sampler2D positionsTexture;
uniform sampler2D polygonPathTexture; // Texture containing polygon path points
uniform int polygonPathLength;
uniform float spaceSize;
uniform vec2 screenSize;
uniform mat3 transformationMatrix;

varying vec2 textureCoords;

// Get a point from the polygon path texture at a specific index
vec2 getPolygonPoint(sampler2D pathTexture, int index, int pathLength) {
  if (index >= pathLength) return vec2(0.0);

  // Calculate texture coordinates for the index
  int textureSize = int(ceil(sqrt(float(pathLength))));
  int x = index - (index / textureSize) * textureSize;
  int y = index / textureSize;

  vec2 texCoord = (vec2(float(x), float(y)) + 0.5) / float(textureSize);
  vec4 pathData = texture2D(pathTexture, texCoord);

  return pathData.xy;
}

// Point-in-polygon algorithm using ray casting
bool pointInPolygon(vec2 point, sampler2D pathTexture, int pathLength) {
  bool inside = false;

  for (int i = 0; i < 2048; i++) {
    if (i >= pathLength) break;

    int j = int(mod(float(i + 1), float(pathLength)));

    vec2 pi = getPolygonPoint(pathTexture, i, pathLength);
    vec2 pj = getPolygonPoint(pathTexture, j, pathLength);

    if (((pi.y > point.y) != (pj.y > point.y)) &&
        (point.x < (pj.x - pi.x) * (point.y - pi.y) / (pj.y - pi.y) + pi.x)) {
      inside = !inside;
    }
  }

  return inside;
}

void main() {
  vec4 pointPosition = texture2D(positionsTexture, textureCoords);
  vec2 p = 2.0 * pointPosition.rg / spaceSize - 1.0;
  p *= spaceSize / screenSize;
  vec3 final = transformationMatrix * vec3(p, 1);

  // Convert to screen coordinates for polygon check
  vec2 screenPos = (final.xy + 1.0) * screenSize / 2.0;

  gl_FragColor = vec4(0.0, 0.0, pointPosition.rg);

  // Check if point center is inside the polygon
  if (pointInPolygon(screenPos, polygonPathTexture, polygonPathLength)) {
    gl_FragColor.r = 1.0;
  }
}`

const findHoveredPointVert = `#ifdef GL_ES
precision highp float;
#endif

attribute float size;

uniform sampler2D positionsTexture;
uniform float pointsTextureSize;
uniform float sizeScale;
uniform float spaceSize;
uniform vec2 screenSize;
uniform float ratio;
uniform mat3 transformationMatrix;
uniform vec2 mousePosition;
uniform bool scalePointsOnZoom;
uniform float maxPointSize;

attribute vec2 pointIndices;

varying vec4 rgba;

float calculatePointSize(float size) {
  float pSize;
  if (scalePointsOnZoom) {
    pSize = size * ratio * transformationMatrix[0][0];
  } else {
    pSize = size * ratio * min(5.0, max(1.0, transformationMatrix[0][0] * 0.01));
  }
  return min(pSize, maxPointSize * ratio);
}

float euclideanDistance (float x1, float x2, float y1, float y2) {
  return sqrt((x2 - x1) * (x2 - x1) + (y2 - y1) * (y2 - y1));
}

void main() {
  vec4 pointPosition = texture2D(positionsTexture, (pointIndices + 0.5) / pointsTextureSize);

  // Transform point position to normalized device coordinates
  vec2 normalizedPoint = 2.0 * pointPosition.rg / spaceSize - 1.0;
  normalizedPoint *= spaceSize / screenSize;
  vec3 finalPosition = transformationMatrix * vec3(normalizedPoint, 1);

  float pointRadius = 0.5 * calculatePointSize(size * sizeScale);

  vec2 pointScreenPosition = (finalPosition.xy + 1.0) * screenSize / 2.0;
  rgba = vec4(0.0);
  gl_Position = vec4(0.5, 0.5, 0.0, 1.0);
  // Check if the mouse is within the point radius
  if (euclideanDistance(pointScreenPosition.x, mousePosition.x, pointScreenPosition.y, mousePosition.y) < pointRadius / ratio) {
    float index = pointIndices.g * pointsTextureSize + pointIndices.r;
    rgba = vec4(index, size, pointPosition.xy);
    gl_Position = vec4(-0.5, -0.5, 0.0, 1.0);
  }

  gl_PointSize = 1.0;
}`

const findHoveredPointFrag = `#ifdef GL_ES
precision highp float;
#endif

varying vec4 rgba;

void main() {
  gl_FragColor = rgba;
}`

const fillSampledPointsVert = `#ifdef GL_ES
precision highp float;
#endif

uniform sampler2D positionsTexture;
uniform float pointsTextureSize;
uniform float spaceSize;
uniform vec2 screenSize;
uniform mat3 transformationMatrix;

attribute vec2 pointIndices;

varying vec4 rgba;

void main() {
  vec4 pointPosition = texture2D(positionsTexture, (pointIndices + 0.5) / pointsTextureSize);
  vec2 p = 2.0 * pointPosition.rg / spaceSize - 1.0;
  p *= spaceSize / screenSize;
  vec3 final = transformationMatrix * vec3(p, 1);

  vec2 pointScreenPosition = (final.xy + 1.0) * screenSize / 2.0;
  float index = pointIndices.g * pointsTextureSize + pointIndices.r;
  rgba = vec4(index, 1.0, pointPosition.xy);
  float i = (pointScreenPosition.x + 0.5) / screenSize.x;
  float j = (pointScreenPosition.y + 0.5) / screenSize.y;
  gl_Position = vec4(2.0 * vec2(i, j) - 1.0, 0.0, 1.0);

  gl_PointSize = 1.0;
}`

const fillSampledPointsFrag = `#ifdef GL_ES
precision highp float;
#endif

varying vec4 rgba;

void main() {
  gl_FragColor = rgba;
}`

const trackPositionsFrag = `#ifdef GL_ES
precision highp float;
#endif

uniform sampler2D positionsTexture;
uniform sampler2D trackedIndices;
uniform float pointsTextureSize;

varying vec2 textureCoords;

void main() {
  vec4 trackedPointIndices = texture2D(trackedIndices, textureCoords);
  if (trackedPointIndices.r < 0.0) discard;
  vec4 pointPosition = texture2D(positionsTexture, (trackedPointIndices.rg + 0.5) / pointsTextureSize);

  gl_FragColor = vec4(pointPosition.rg, 1.0, 1.0);
}`

const drawLineVert = `precision highp float;

attribute vec2 position, pointA, pointB;
attribute vec4 color;
attribute float width;
attribute float arrow;
attribute float linkIndices;

uniform sampler2D positionsTexture;
uniform sampler2D pointGreyoutStatus;
uniform mat3 transformationMatrix;
uniform float pointsTextureSize;
uniform float widthScale;
uniform float linkArrowsSizeScale;
uniform float spaceSize;
uniform vec2 screenSize;
uniform vec2 linkVisibilityDistanceRange;
uniform float linkVisibilityMinTransparency;
uniform float linkOpacity;
uniform float greyoutOpacity;
uniform float curvedWeight;
uniform float curvedLinkControlPointDistance;
uniform float curvedLinkSegments;
uniform bool scaleLinksOnZoom;
uniform float maxPointSize;
// renderMode: 0.0 = normal rendering, 1.0 = index buffer rendering for picking
uniform float renderMode;
uniform float hoveredLinkIndex;
uniform vec4 hoveredLinkColor;
uniform float hoveredLinkWidthIncrease;

varying vec4 rgbaColor;
varying vec2 pos;
varying float arrowLength;
varying float useArrow;
varying float smoothing;
varying float arrowWidthFactor;
varying float linkIndex;

float map(float value, float min1, float max1, float min2, float max2) {
  return min2 + (value - min1) * (max2 - min2) / (max1 - min1);
}

vec2 conicParametricCurve(vec2 A, vec2 B, vec2 ControlPoint, float t, float w) {
  vec2 divident = (1.0 - t) * (1.0 - t) * A + 2.0 * (1.0 - t) * t * w * ControlPoint + t * t * B;
  float divisor = (1.0 - t) * (1.0 - t) + 2.0 * (1.0 - t) * t * w + t * t;
  return divident / divisor;
}

float calculateLinkWidth(float width) {
  float linkWidth;
  if (scaleLinksOnZoom) {
    // Use original width if links should scale with zoom
    linkWidth = width;
  } else {
    // Adjust width based on zoom level to maintain visual size
    linkWidth = width / transformationMatrix[0][0];
    // Apply a non-linear scaling to avoid extreme widths
    linkWidth *= min(5.0, max(1.0, transformationMatrix[0][0] * 0.01));
  }
  // Limit link width based on whether it has an arrow
  if (useArrow > 0.5) {
    return min(linkWidth, (maxPointSize * 2.0) / transformationMatrix[0][0]);
  } else {
    return min(linkWidth, maxPointSize / transformationMatrix[0][0]);
  }
}

float calculateArrowWidth(float arrowWidth) {
  if (scaleLinksOnZoom) {
    return arrowWidth;
  } else {
    // Apply the same scaling logic as calculateLinkWidth to maintain proportionality
    arrowWidth = arrowWidth / transformationMatrix[0][0];
    // Apply the same non-linear scaling to avoid extreme widths
    arrowWidth *= min(5.0, max(1.0, transformationMatrix[0][0] * 0.01));
    return arrowWidth;
  }
}

void main() {
  pos = position;
  linkIndex = linkIndices;

  vec2 pointTexturePosA = (pointA + 0.5) / pointsTextureSize;
  vec2 pointTexturePosB = (pointB + 0.5) / pointsTextureSize;

  vec4 greyoutStatusA = texture2D(pointGreyoutStatus, pointTexturePosA);
  vec4 greyoutStatusB = texture2D(pointGreyoutStatus, pointTexturePosB);

  vec4 pointPositionA = texture2D(positionsTexture, pointTexturePosA);
  vec4 pointPositionB = texture2D(positionsTexture, pointTexturePosB);
  vec2 a = pointPositionA.xy;
  vec2 b = pointPositionB.xy;

  // Calculate direction vector and its perpendicular
  vec2 xBasis = b - a;
  vec2 yBasis = normalize(vec2(-xBasis.y, xBasis.x));

  // Calculate link distance and control point for curved link
  float linkDist = length(xBasis);
  float h = curvedLinkControlPointDistance;
  vec2 controlPoint = (a + b) / 2.0 + yBasis * linkDist * h;

  // Convert link distance to screen pixels
  float linkDistPx = linkDist * transformationMatrix[0][0];

  // Calculate line width using the width scale
  float linkWidth = width * widthScale;
  float k = 2.0;
  // Arrow width is proportionally larger than the line width
  float arrowWidth = linkWidth * k;
  arrowWidth *= linkArrowsSizeScale;

  // Ensure arrow width difference is non-negative to prevent unwanted changes to link width
  float arrowWidthDifference = max(0.0, arrowWidth - linkWidth);

  // Calculate arrow width in pixels
  float arrowWidthPx = calculateArrowWidth(arrowWidth);

  // Calculate arrow length proportional to its width
  // 0.866 is approximately sqrt(3)/2 - related to equilateral triangle geometry
  // Cap the length to avoid overly long arrows on short links
  arrowLength = min(0.3, (0.866 * arrowWidthPx * 2.0) / linkDist);

  useArrow = arrow;
  if (useArrow > 0.5) {
    linkWidth += arrowWidthDifference;
  }

  arrowWidthFactor = arrowWidthDifference / linkWidth;

  // Calculate final link width in pixels with smoothing
  float linkWidthPx = calculateLinkWidth(linkWidth);

  if (renderMode > 0.0) {
    // Add 5 pixels padding for better hover detection
    linkWidthPx += 5.0 / transformationMatrix[0][0];
  } else {
      // Add pixel increase if this is the hovered link
    if (hoveredLinkIndex == linkIndex) {
      linkWidthPx += hoveredLinkWidthIncrease / transformationMatrix[0][0];
    }
  }
  float smoothingPx = 0.5 / transformationMatrix[0][0];
  smoothing = smoothingPx / linkWidthPx;
  linkWidthPx += smoothingPx;



  // Calculate final color with opacity based on link distance
  vec3 rgbColor = color.rgb;
  // Adjust opacity based on link distance
  float opacity = color.a * linkOpacity * max(linkVisibilityMinTransparency, map(linkDistPx, linkVisibilityDistanceRange.g, linkVisibilityDistanceRange.r, 0.0, 1.0));

  // Apply greyed out opacity if either endpoint is greyed out
  if (greyoutStatusA.r > 0.0 || greyoutStatusB.r > 0.0) {
    opacity *= greyoutOpacity;
  }

  // Pass final color to fragment shader
  rgbaColor = vec4(rgbColor, opacity);

  // Apply hover color if this is the hovered link and hover color is defined
  if (hoveredLinkIndex == linkIndex && hoveredLinkColor.a > -0.5) {
    // Keep existing RGB values but multiply opacity with hover color opacity
    rgbaColor.rgb = hoveredLinkColor.rgb;
    rgbaColor.a *= hoveredLinkColor.a;
  }

  // Calculate position on the curved path
  float t = position.x;
  float w = curvedWeight;

  float tPrev = t - 1.0 / curvedLinkSegments;
  float tNext = t + 1.0 / curvedLinkSegments;

  vec2 pointCurr = conicParametricCurve(a, b, controlPoint, t, w);

  vec2 pointPrev = conicParametricCurve(a, b, controlPoint, max(0.0, tPrev), w);
  vec2 pointNext = conicParametricCurve(a, b, controlPoint, min(tNext, 1.0), w);

  vec2 xBasisCurved = pointNext - pointPrev;
  vec2 yBasisCurved = normalize(vec2(-xBasisCurved.y, xBasisCurved.x));

  pointCurr += yBasisCurved * linkWidthPx * position.y;

  // Transform to clip space coordinates
  vec2 p = 2.0 * pointCurr / spaceSize - 1.0;
  p *= spaceSize / screenSize;
  vec3 final = transformationMatrix * vec3(p, 1);

  gl_Position = vec4(final.rg, 0, 1);
}`

const drawLineFrag = `precision highp float;

varying vec4 rgbaColor;
varying vec2 pos;
varying float arrowLength;
varying float useArrow;
varying float smoothing;
varying float arrowWidthFactor;
varying float linkIndex;

// renderMode: 0.0 = normal rendering, 1.0 = index buffer rendering for picking
uniform float renderMode;

float map(float value, float min1, float max1, float min2, float max2) {
  return min2 + (value - min1) * (max2 - min2) / (max1 - min1);
}

void main() {
  float opacity = 1.0;
  vec3 color = rgbaColor.rgb;

  if (useArrow > 0.5) {
    float end_arrow = 0.5 + arrowLength / 2.0;
    float start_arrow = end_arrow - arrowLength;
    float arrowWidthDelta = arrowWidthFactor / 2.0;
    float linkOpacity = rgbaColor.a * smoothstep(0.5 - arrowWidthDelta, 0.5 - arrowWidthDelta - smoothing / 2.0, abs(pos.y));
    float arrowOpacity = 1.0;
    if (pos.x > start_arrow && pos.x < start_arrow + arrowLength) {
      float xmapped = map(pos.x, start_arrow, end_arrow, 0.0, 1.0);
      arrowOpacity = rgbaColor.a * smoothstep(xmapped - smoothing, xmapped, map(abs(pos.y), 0.5, 0.0, 0.0, 1.0));
      if (linkOpacity != arrowOpacity) {
        linkOpacity = max(linkOpacity, arrowOpacity);
      }
    }
    opacity = linkOpacity;
  } else opacity = rgbaColor.a * smoothstep(0.5, 0.5 - smoothing, abs(pos.y));

  if (renderMode > 0.0) {
    if (opacity > 0.0) {
      gl_FragColor = vec4(linkIndex, 0.0, 0.0, 1.0);
    } else {
      gl_FragColor = vec4(-1.0, 0.0, 0.0, 0.0);
    }
  } else gl_FragColor = vec4(color, opacity);

}`

const hoveredLineIndexVert = `attribute vec2 position;

varying vec2 vTexCoord;

void main() {
  vTexCoord = position * 0.5 + 0.5;
  gl_Position = vec4(position, 0.0, 1.0);
}`

const hoveredLineIndexFrag = `precision highp float;

uniform sampler2D linkIndexTexture;
uniform vec2 mousePosition;
uniform vec2 screenSize;

varying vec2 vTexCoord;

void main() {
  // Convert mouse position to texture coordinates
  vec2 texCoord = mousePosition / screenSize;

  // Read the link index from the linkIndexFbo texture at mouse position
  vec4 linkIndexData = texture2D(linkIndexTexture, texCoord);

  // Extract the link index (stored in the red channel)
  float linkIndex = linkIndexData.r;

  // Check if there's a valid link at this position (alpha > 0)
  if (linkIndexData.a > 0.0 && linkIndex >= 0.0) {
    // Output the link index
    gl_FragColor = vec4(linkIndex, 0.0, 0.0, 1.0);
  } else {
    // No link at this position, output -1 to indicate no hover
    gl_FragColor = vec4(-1.0, 0.0, 0.0, 0.0);
  }
}`

const forceGravityFrag = `#ifdef GL_ES
precision highp float;
#endif

uniform sampler2D positionsTexture;
uniform float gravity;
uniform float spaceSize;
uniform float alpha;

varying vec2 textureCoords;

void main() {
  vec4 pointPosition = texture2D(positionsTexture, textureCoords);

  vec4 velocity = vec4(0.0);

  vec2 centerPosition = vec2(spaceSize / 2.0);
  vec2 distVector = centerPosition - pointPosition.rg;
  float dist = sqrt(dot(distVector, distVector));
  if (dist > 0.0) {
    float angle = atan(distVector.y, distVector.x);
    float additionalVelocity = alpha * gravity * dist * 0.1;
    velocity.rg += additionalVelocity * vec2(cos(angle), sin(angle));
  }

  gl_FragColor = velocity;
}`

const calculateCentermassVert = `#ifdef GL_ES
precision highp float;
#endif

uniform sampler2D positionsTexture;
uniform float pointsTextureSize;

attribute vec2 pointIndices;

varying vec4 rgba;

void main() {
  vec4 pointPosition = texture2D(positionsTexture, pointIndices / pointsTextureSize);
  rgba = vec4(pointPosition.xy, 1.0, 0.0);

  gl_Position = vec4(0.0, 0.0, 0.0, 1.0);
  gl_PointSize = 1.0;
}`

const calculateCentermassFrag = `#ifdef GL_ES
precision highp float;
#endif

varying vec4 rgba;

void main() {
  gl_FragColor = rgba;
}`

const forceCenterFrag = `#ifdef GL_ES
precision highp float;
#endif

uniform sampler2D positionsTexture;
uniform sampler2D centermassTexture;
uniform float centerForce;
uniform float alpha;

varying vec2 textureCoords;


void main() {
  vec4 pointPosition = texture2D(positionsTexture, textureCoords);
  vec4 velocity = vec4(0.0);
  vec4 centermassValues = texture2D(centermassTexture, vec2(0.0));
  vec2 centermassPosition = centermassValues.xy / centermassValues.b;
  vec2 distVector = centermassPosition - pointPosition.xy;
  float dist = sqrt(dot(distVector, distVector));
  if (dist > 0.0) {
    float angle = atan(distVector.y, distVector.x);
    float addV = alpha * centerForce * dist * 0.01;
    velocity.rg += addV * vec2(cos(angle), sin(angle));
  }

  gl_FragColor = velocity;
}`

const forceMouseFrag = `#ifdef GL_ES
precision highp float;
#endif

uniform sampler2D positionsTexture;
uniform float repulsion;
uniform vec2 mousePos;

varying vec2 textureCoords;

void main() {
  vec4 pointPosition = texture2D(positionsTexture, textureCoords);
  vec4 velocity = vec4(0.0);
  vec2 mouse = mousePos;
  // Move particles away from the mouse position using a repulsive force
  vec2 distVector = mouse - pointPosition.rg;
  float dist = sqrt(dot(distVector, distVector));
  dist = max(dist, 10.0);
  float angle = atan(distVector.y, distVector.x);
  float addV = 100.0 * repulsion / (dist * dist);
  velocity.rg -= addV * vec2(cos(angle), sin(angle));

  gl_FragColor = velocity;
}`

const calculateLevelVert = `#ifdef GL_ES
precision highp float;
#endif

uniform sampler2D positionsTexture;
uniform float pointsTextureSize;
uniform float levelTextureSize;
uniform float cellSize;

attribute vec2 pointIndices;

varying vec4 rgba;

void main() {
  vec4 pointPosition = texture2D(positionsTexture, pointIndices / pointsTextureSize);
  rgba = vec4(pointPosition.rg, 1.0, 0.0);

  float n = floor(pointPosition.x / cellSize);
  float m = floor(pointPosition.y / cellSize);

  vec2 levelPosition = 2.0 * (vec2(n, m) + 0.5) / levelTextureSize - 1.0;

  gl_Position = vec4(levelPosition, 0.0, 1.0);
  gl_PointSize = 1.0;
}`

const calculateLevelFrag = `#ifdef GL_ES
precision highp float;
#endif

varying vec4 rgba;

void main() {
  gl_FragColor = rgba;
}`

const forceLevelFrag = `// The fragment shader calculates the velocity of a point based on its position
// and the positions of other points in different levels of a spatial hierarchy.

#ifdef GL_ES
precision highp float;
#endif

uniform sampler2D positionsTexture;
uniform sampler2D levelFbo;

uniform float level;
uniform float levels;
uniform float levelTextureSize;
uniform float repulsion;
uniform float alpha;
uniform float spaceSize;
uniform float theta;

varying vec2 textureCoords;

const float MAX_LEVELS_NUM = 14.0;

vec2 calculateAdditionalVelocity (vec2 ij, vec2 pp) {
  vec2 add = vec2(0.0);
  vec4 centermass = texture2D(levelFbo, ij);
  if (centermass.r > 0.0 && centermass.g > 0.0 && centermass.b > 0.0) {
    vec2 centermassPosition = vec2(centermass.rg / centermass.b);
    vec2 distVector = pp - centermassPosition;
    float l = dot(distVector, distVector);
    float dist = sqrt(l);
    if (l > 0.0) {
      float c = alpha * repulsion * centermass.b;

      float distanceMin2 = 1.0;
      if (l < distanceMin2) l = sqrt(distanceMin2 * l);
      float addV = c / sqrt(l);
      add = addV * normalize(distVector);
    }
  }
  return add;
}

void main() {
  vec4 pointPosition = texture2D(positionsTexture, textureCoords);
  float x = pointPosition.x;
  float y = pointPosition.y;

  float left = 0.0;
  float top = 0.0;
  float right = spaceSize;
  float bottom = spaceSize;

  float n_left = 0.0;
  float n_top = 0.0;
  float n_right = 0.0;
  float n_bottom = 0.0;

  float cellSize = 0.0;

  // Iterate over levels to adjust the boundaries based on the current level
  for (float i = 0.0; i < MAX_LEVELS_NUM; i += 1.0) {
    if (i <= level) {
      left += cellSize * n_left;
      top += cellSize * n_top;
      right -= cellSize * n_right;
      bottom -= cellSize * n_bottom;

      cellSize = pow(2.0 , levels - i - 1.0);

      float dist_left = x - left;
      n_left = max(0.0, floor(dist_left / cellSize - theta));

      float dist_top = y - top;
      n_top = max(0.0, floor(dist_top / cellSize - theta));

      float dist_right = right - x;
      n_right = max(0.0, floor(dist_right / cellSize - theta));

      float dist_bottom = bottom - y;
      n_bottom = max(0.0, floor(dist_bottom / cellSize - theta));

    }
  }

  vec4 velocity = vec4(vec2(0.0), 1.0, 0.0);

  // Calculate the additional velocity based on neighboring cells
  for (float i = 0.0; i < 12.0; i += 1.0) {
    for (float j = 0.0; j < 4.0; j += 1.0) {
      float n = left + cellSize * j;
      float m = top + cellSize * n_top + cellSize * i;

      if (n < (left + n_left * cellSize) && m < bottom) {
        velocity.xy += calculateAdditionalVelocity(vec2(n / cellSize, m / cellSize) / levelTextureSize, pointPosition.xy);
      }

      n = left + cellSize * i;
      m = top + cellSize * j;

      if (n < (right - n_right * cellSize) && m < (top + n_top * cellSize)) {
        velocity.xy += calculateAdditionalVelocity(vec2(n / cellSize, m / cellSize) / levelTextureSize, pointPosition.xy);
      }

      n = right - n_right * cellSize + cellSize * j;
      m = top + cellSize * i;

      if (n < right && m < (bottom - n_bottom * cellSize)) {
        velocity.xy += calculateAdditionalVelocity(vec2(n / cellSize, m / cellSize) / levelTextureSize, pointPosition.xy);
      }

      n = left + n_left * cellSize + cellSize * i;
      m = bottom - n_bottom * cellSize + cellSize * j;

      if (n < right && m < bottom) {
        velocity.xy += calculateAdditionalVelocity(vec2(n / cellSize, m / cellSize) / levelTextureSize, pointPosition.xy);
      }
    }
  }

  gl_FragColor = velocity;
}`

const forceCentermassFrag = `// The fragment shader calculates the velocity of a point based on its position,
// the positions of other points in a spatial hierarchy, and random factors.

#ifdef GL_ES
precision highp float;
#endif

uniform sampler2D positionsTexture;
uniform sampler2D levelFbo;
uniform sampler2D randomValues;

uniform float levelTextureSize;
uniform float repulsion;
uniform float alpha;

varying vec2 textureCoords;

// Calculate the additional velocity based on the center of mass
vec2 calculateAdditionalVelocity (vec2 ij, vec2 pp) {
  vec2 add = vec2(0.0);
  vec4 centermass = texture2D(levelFbo, ij);
  if (centermass.r > 0.0 && centermass.g > 0.0 && centermass.b > 0.0) {
    vec2 centermassPosition = vec2(centermass.rg / centermass.b);
    vec2 distVector = pp - centermassPosition;
    float l = dot(distVector, distVector);
    float dist = sqrt(l);
    if (l > 0.0) {
      float angle = atan(distVector.y, distVector.x);
      float c = alpha * repulsion * centermass.b;

      float distanceMin2 = 1.0;
      if (l < distanceMin2) l = sqrt(distanceMin2 * l);
      float addV = c / sqrt(l);
      add = addV * vec2(cos(angle), sin(angle));
    }
  }
  return add;
}

void main() {
  vec4 pointPosition = texture2D(positionsTexture, textureCoords);
  vec4 random = texture2D(randomValues, textureCoords);

  vec4 velocity = vec4(0.0);

  // Calculate additional velocity based on the point position
  velocity.xy += calculateAdditionalVelocity(pointPosition.xy / levelTextureSize, pointPosition.xy);
  // Apply random factor to the velocity
  velocity.xy += velocity.xy * random.rg;

  gl_FragColor = velocity;
}`

const clusterCentermassVert = `#ifdef GL_ES
precision highp float;
#endif

uniform sampler2D positionsTexture;
uniform sampler2D clusterTexture;
uniform float pointsTextureSize;
uniform float clustersTextureSize;

attribute vec2 pointIndices;

varying vec4 rgba;

void main() {
  vec4 pointPosition = texture2D(positionsTexture, pointIndices / pointsTextureSize);
  rgba = vec4(pointPosition.xy, 1.0, 0.0);

  vec4 pointClusterIndices = texture2D(clusterTexture, pointIndices / pointsTextureSize);
  vec2 xy = vec2(0.0);
  if (pointClusterIndices.x >= 0.0 && pointClusterIndices.y >= 0.0) {
    xy = 2.0 * (pointClusterIndices.xy + 0.5) / clustersTextureSize - 1.0;
  }

  gl_Position = vec4(xy, 0.0, 1.0);
  gl_PointSize = 1.0;
}`

const clusterCentermassFrag = `#ifdef GL_ES
precision highp float;
#endif

varying vec4 rgba;

void main() {
  gl_FragColor = rgba;
}`

const forceClusterFrag = `#ifdef GL_ES
precision highp float;
#endif

uniform sampler2D positionsTexture;
uniform sampler2D centermassTexture;
uniform sampler2D clusterTexture;
uniform sampler2D clusterPositionsTexture;
uniform sampler2D clusterForceCoefficient;
uniform float alpha;
uniform float clustersTextureSize;
uniform float clusterCoefficient;

varying vec2 textureCoords;


void main() {
  vec4 pointPosition = texture2D(positionsTexture, textureCoords);
  vec4 velocity = vec4(0.0);
  vec4 pointClusterIndices = texture2D(clusterTexture, textureCoords);
  // no cluster, so no forces
  if (pointClusterIndices.x >= 0.0 && pointClusterIndices.y >= 0.0) {
    // positioning points to custom cluster position or either to the center of mass
    vec2 clusterPositions = texture2D(clusterPositionsTexture, pointClusterIndices.xy / clustersTextureSize).xy;
    if (clusterPositions.x < 0.0 || clusterPositions.y < 0.0) {
      vec4 centermassValues = texture2D(centermassTexture, pointClusterIndices.xy / clustersTextureSize);
      clusterPositions = centermassValues.xy / centermassValues.b;
    }
    vec4 clusterCustomCoeff = texture2D(clusterForceCoefficient, textureCoords);
    vec2 distVector = clusterPositions.xy - pointPosition.xy;
    float dist = length(distVector);
    if (dist > 0.0) {
      float addV = alpha * dist * clusterCoefficient * clusterCustomCoeff.r;
      velocity.rg += addV * normalize(distVector);
    }
  }

  gl_FragColor = velocity;
}`

// forceSpringFrag generates the ForceLink shader; maxLinks is inlined as a
// loop bound (WebGL 1 requires constant loop bounds), mirroring
// ForceLink/force-spring.ts.
func forceSpringFrag(maxLinks int) string {
	return fmt.Sprintf(`
#ifdef GL_ES
precision highp float;
#endif

uniform sampler2D positionsTexture;
uniform float linkSpring;
uniform float linkDistance;
uniform vec2 linkDistRandomVariationRange;

uniform sampler2D linkInfoTexture; // Texture storing first link indices and amount
uniform sampler2D linkIndicesTexture;
uniform sampler2D linkPropertiesTexture; // Texture storing link bias and strength
uniform sampler2D linkRandomDistanceTexture;

uniform float pointsTextureSize;
uniform float linksTextureSize;
uniform float alpha;

varying vec2 textureCoords;

const float MAX_LINKS = %d.0;

void main() {
  vec4 pointPosition = texture2D(positionsTexture, textureCoords);
  vec4 velocity = vec4(0.0);

  vec4 linkInfo = texture2D(linkInfoTexture, textureCoords);
  float iCount = linkInfo.r;
  float jCount = linkInfo.g;
  float linkAmount = linkInfo.b;
  if (linkAmount > 0.0) {
    for (float i = 0.0; i < MAX_LINKS; i += 1.0) {
      if (i < linkAmount) {
        if (iCount >= linksTextureSize) {
          iCount = 0.0;
          jCount += 1.0;
        }
        vec2 linkTextureIndex = (vec2(iCount, jCount) + 0.5) / linksTextureSize;
        vec4 connectedPointIndex = texture2D(linkIndicesTexture, linkTextureIndex);
        vec4 biasAndStrength = texture2D(linkPropertiesTexture, linkTextureIndex);
        vec4 randomMinDistance = texture2D(linkRandomDistanceTexture, linkTextureIndex);
        float bias = biasAndStrength.r;
        float strength = biasAndStrength.g;
        float randomMinLinkDist = randomMinDistance.r * (linkDistRandomVariationRange.g - linkDistRandomVariationRange.r) + linkDistRandomVariationRange.r;
        randomMinLinkDist *= linkDistance;

        iCount += 1.0;

        vec4 connectedPointPosition = texture2D(positionsTexture, (connectedPointIndex.rg + 0.5) / pointsTextureSize);
        float x = connectedPointPosition.x - (pointPosition.x + velocity.x);
        float y = connectedPointPosition.y - (pointPosition.y + velocity.y);
        float l = sqrt(x * x + y * y);

        // Apply the link force
        l = max(l, randomMinLinkDist * 0.99);
        l = (l - randomMinLinkDist) / l;
        l *= linkSpring * alpha;
        l *= strength;
        l *= bias;
        x *= l;
        y *= l;
        velocity.x += x;
        velocity.y += y;
      }
    }
  }

  gl_FragColor = vec4(velocity.rg, 0.0, 0.0);
}
  `, maxLinks)
}

// quadtreeFrag generates the Barnes–Hut quadtree force shader, mirroring
// the recursive string generation in quadtree-frag-shader.ts.
func quadtreeFrag(startLevel, maxLevels int) string {
	if startLevel > maxLevels {
		startLevel = maxLevels
	}
	delta := maxLevels - startLevel
	calcAdd := `
    float dist = sqrt(l);
    if (dist > 0.0) {
      float c = alpha * repulsion * centermass.b;
      addVelocity += calculateAdditionalVelocity(vec2(x, y), l, c);
      addVelocity += addVelocity * random.rg;
    }
  `
	var quad func(level int) string
	quad = func(level int) string {
		if level >= maxLevels {
			return calcAdd
		}
		groupSize := 1 << uint(level+1)
		var iParts, jParts []string
		for l := 0; l < level+1-delta; l++ {
			iParts = append(iParts, fmt.Sprintf("pow(2.0, %d.0) * i%d", level-(l+delta), l+delta))
			jParts = append(jParts, fmt.Sprintf("pow(2.0, %d.0) * j%d", level-(l+delta), l+delta))
		}
		iEnding := strings.Join(iParts, "+")
		jEnding := strings.Join(jParts, "+")

		return fmt.Sprintf(`
      for (float ij%[1]d = 0.0; ij%[1]d < 4.0; ij%[1]d += 1.0) {
        float i%[1]d = 0.0;
        float j%[1]d = 0.0;
        if (ij%[1]d == 1.0 || ij%[1]d == 3.0) i%[1]d = 1.0;
        if (ij%[1]d == 2.0 || ij%[1]d == 3.0) j%[1]d = 1.0;
        float i = pow(2.0, %[2]d.0) * n / width%[3]d + %[4]s;
        float j = pow(2.0, %[2]d.0) * m / width%[3]d + %[5]s;
        float groupPosX = (i + 0.5) / %[6]d.0;
        float groupPosY = (j + 0.5) / %[6]d.0;

        vec4 centermass = texture2D(level[%[1]d], vec2(groupPosX, groupPosY));
        if (centermass.r > 0.0 && centermass.g > 0.0 && centermass.b > 0.0) {
          float x = centermass.r / centermass.b - pointPosition.r;
          float y = centermass.g / centermass.b - pointPosition.g;
          float l = x * x + y * y;
          if ((width%[3]d * width%[3]d) / theta < l) {
            %[7]s
          } else {
            %[8]s
          }
        }
      }
      `, level, startLevel, level+1, iEnding, jEnding, groupSize, calcAdd, quad(level+1))
	}

	var widths strings.Builder
	for i := 0; i < maxLevels; i++ {
		fmt.Fprintf(&widths, "float width%d = width%d / 2.0;\n", i+1, i)
	}

	return fmt.Sprintf(`
#ifdef GL_ES
precision highp float;
#endif

uniform sampler2D positionsTexture;
uniform sampler2D randomValues;
uniform float spaceSize;
uniform float repulsion;
uniform float theta;
uniform float alpha;
uniform sampler2D level[%d];
varying vec2 textureCoords;

vec2 calculateAdditionalVelocity(vec2 xy, float l, float c) {
  float distanceMin2 = 1.0;
  if (l < distanceMin2) l = sqrt(distanceMin2 * l);
  float add = c / l;
  return add * xy;
}

void main() {
  vec4 pointPosition = texture2D(positionsTexture, textureCoords);
  vec4 random = texture2D(randomValues, textureCoords);

  float width0 = spaceSize;

  vec2 velocity = vec2(0.0);
  vec2 addVelocity = vec2(0.0);

  %s

  for (float n = 0.0; n < pow(2.0, %d.0); n += 1.0) {
    for (float m = 0.0; m < pow(2.0, %d.0); m += 1.0) {
      %s
    }
  }

  velocity -= addVelocity;

  gl_FragColor = vec4(velocity, 0.0, 0.0);
}
`, maxLevels, widths.String(), delta, delta, quad(delta))
}
