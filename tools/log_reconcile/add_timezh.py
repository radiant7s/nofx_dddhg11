import argparse
import json
import os
from datetime import datetime, timezone, timedelta
from typing import Any, List


def _to_int(value: Any) -> int:
    if isinstance(value, int):
        return value
    if isinstance(value, float):
        return int(value)
    if isinstance(value, str):
        value = value.strip()
        if value == "":
            raise ValueError("empty string for time")
        return int(value)
    raise TypeError(f"Unsupported time type: {type(value)}")


def _epoch_to_seconds(epoch_value: int) -> int:
    """
    Normalize epoch_value to whole seconds.
    Supports seconds(10+), milliseconds(13+), microseconds(16+), nanoseconds(19+).
    We round down to the nearest second as sub-second precision isn't required.
    """
    n = abs(epoch_value)
    digits = len(str(n))

    if digits >= 19:  # nanoseconds
        return epoch_value // 1_000_000_000
    if digits >= 16:  # microseconds
        return epoch_value // 1_000_000
    if digits >= 13:  # milliseconds
        return epoch_value // 1_000
    # assume seconds
    return epoch_value


def format_with_offset(seconds: int, offset_hours: float) -> str:
    # 输出格式：YYYY-MM-DD HH:MM:SS UTC+HH:MM
    total_minutes = int(round(offset_hours * 60))
    tz = timezone(timedelta(minutes=total_minutes))
    dt = datetime.fromtimestamp(seconds, tz=tz)
    sign = "+" if total_minutes >= 0 else "-"
    minutes = abs(total_minutes)
    hh = minutes // 60
    mm = minutes % 60
    label = f"UTC{sign}{hh:02d}:{mm:02d}"
    return dt.strftime("%Y-%m-%d %H:%M:%S ") + label


def add_timezh(records: List[Any], offset_hours: float) -> int:
    updated = 0
    for obj in records:
        if not isinstance(obj, dict):
            continue
        if "time" not in obj:
            continue
        try:
            raw = _to_int(obj["time"])  # 可能是毫秒、微秒、纳秒
            seconds = _epoch_to_seconds(raw)
            obj["timezh"] = format_with_offset(seconds, offset_hours)
            updated += 1
        except Exception:
            # 跳过无法解析的 time
            continue
    return updated


def main():
    parser = argparse.ArgumentParser(description="为订单数组中每个对象新增 timezh(可视化时间，默认北京时间) 字段")
    parser.add_argument("--input", "-i", default="orders_export.json", help="输入 JSON 文件路径（数组）")
    parser.add_argument("--output", "-o", default=None, help="输出 JSON 文件路径；未指定且 --inplace 时覆盖输入")
    parser.add_argument("--inplace", action="store_true", help="直接覆盖输入文件")
    parser.add_argument("--indent", type=int, default=2, help="输出缩进，默认 2")
    parser.add_argument("--offset-hours", type=float, default=8.0, help="时区偏移（小时），默认 8 表示北京时间 UTC+08:00；例如 0 表示 UTC")
    args = parser.parse_args()

    in_path = os.path.abspath(args.input)
    if not os.path.exists(in_path):
        raise SystemExit(f"输入文件不存在: {in_path}")

    with open(in_path, "r", encoding="utf-8") as f:
        try:
            data = json.load(f)
        except json.JSONDecodeError as e:
            raise SystemExit(f"JSON 解析失败: {e}")

    if not isinstance(data, list):
        raise SystemExit("输入文件的根对象应为数组(list)")

    updated = add_timezh(data, args.offset_hours)

    if args.inplace and args.output is None:
        out_path = in_path
    else:
        out_path = os.path.abspath(args.output or (os.path.splitext(in_path)[0] + ".with_timezh.json"))

    # 写回
    with open(out_path, "w", encoding="utf-8") as f:
        json.dump(data, f, ensure_ascii=False, indent=args.indent)

    print(f"已更新对象数量: {updated}")
    print(f"已写入: {out_path}")


if __name__ == "__main__":
    main()
