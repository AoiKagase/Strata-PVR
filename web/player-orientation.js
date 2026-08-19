(function () {
  "use strict";

  function playerScreenOrientation() {
    return window.screen && window.screen.orientation ? window.screen.orientation : null;
  }

  function lockPlayerOrientation() {
    var orientation = playerScreenOrientation();
    if (!orientation || typeof orientation.lock !== "function") {
      return;
    }
    try {
      var request = orientation.lock("landscape");
      if (request && typeof request.catch === "function") {
        request.catch(function () {
          // Orientation locking is an enhancement; playback remains available.
        });
      }
    } catch (error) {
      // Some browsers expose the API but reject calls outside their supported mode.
    }
  }

  function unlockPlayerOrientation() {
    var orientation = playerScreenOrientation();
    if (!orientation || typeof orientation.unlock !== "function") {
      return;
    }
    try {
      orientation.unlock();
    } catch (error) {
      // Unlocking is best effort because the browser may not own the orientation.
    }
  }

  function playerFullscreenElement() {
    return document.fullscreenElement || document.webkitFullscreenElement || document.webkitCurrentFullScreenElement;
  }

  function syncPlayerOrientation() {
    if (playerFullscreenElement()) {
      lockPlayerOrientation();
    } else {
      unlockPlayerOrientation();
    }
  }

  document.addEventListener("fullscreenchange", syncPlayerOrientation);
  document.addEventListener("webkitfullscreenchange", syncPlayerOrientation);
  var videos = document.querySelectorAll("video");
  for (var index = 0; index < videos.length; index += 1) {
    var video = videos[index];
    video.addEventListener("webkitbeginfullscreen", lockPlayerOrientation);
    video.addEventListener("webkitendfullscreen", unlockPlayerOrientation);
  }
})();
