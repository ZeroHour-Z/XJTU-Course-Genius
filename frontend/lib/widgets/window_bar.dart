import 'dart:io' show Platform;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import '../theme/app_theme.dart';

const _channel = MethodChannel('com.xjtu.genius/ime');

/// Width reserved for the native macOS traffic-light buttons, which are drawn by
/// AppKit over our title bar (the window uses fullSizeContentView).
const double _macTrafficLightsWidth = 78;

class WindowBar extends StatelessWidget {
  final Widget? leading;
  const WindowBar({super.key, this.leading});

  @override
  Widget build(BuildContext context) {
    // macOS keeps its native close/minimize/zoom buttons, so we neither draw
    // our own nor let content sit underneath them.
    final isMacOS = Platform.isMacOS;

    return SizedBox(
      height: 40,
      child: Row(
        children: [
          if (isMacOS) const SizedBox(width: _macTrafficLightsWidth),
          if (leading != null) leading!,
          Expanded(
            child: Listener(
              behavior: HitTestBehavior.translucent,
              onPointerDown: (_) => _channel.invokeMethod('windowDrag'),
            ),
          ),
          if (!isMacOS) ...[
            const _WinBtn(Icons.minimize_rounded, 'windowMinimize'),
            const _WinBtn(Icons.crop_square_rounded, 'windowMaximize'),
            const _WinBtn(Icons.close_rounded, 'windowClose', isClose: true),
          ],
        ],
      ),
    );
  }
}

class _WinBtn extends StatelessWidget {
  final IconData icon;
  final String method;
  final bool isClose;
  const _WinBtn(this.icon, this.method, {this.isClose = false});

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      width: 44, height: 40,
      child: InkWell(
        onTap: () => _channel.invokeMethod(method),
        child: Center(
          child: Icon(icon, size: 18,
              color: isClose ? dangerColor : textMuted),
        ),
      ),
    );
  }
}
