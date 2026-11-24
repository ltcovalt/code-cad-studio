from __future__ import annotations

from dataclasses import dataclass, field
import types
from typing import Optional

from params import default_preset, param, param_list
import worker


def test_unwrap_optional_supports_pep604_types_union_type() -> None:
    pep604_float = float | None
    assert isinstance(pep604_float, types.UnionType)
    assert worker._unwrap_optional(pep604_float) == (float, True)

    pep604_bool = bool | None
    assert isinstance(pep604_bool, types.UnionType)
    assert worker._unwrap_optional(pep604_bool) == (bool, True)

    pep604_multi = int | str
    assert isinstance(pep604_multi, types.UnionType)
    assert worker._unwrap_optional(pep604_multi) == (pep604_multi, False)

    assert worker._unwrap_optional(Optional[str]) == (str, True)


def test_bare_list_tuple_and_optional_bool_coercion() -> None:
    # Bare list
    assert worker._coerce_value("[1, 2, 3]", list, []) == [1, 2, 3]
    assert worker._coerce_value("a, b, c", list, []) == ["a", "b", "c"]
    assert worker._coerce_value([4, 5], list, []) == [4, 5]
    assert worker._coerce_value("", list, ["x"]) == ["x"]
    assert worker._coerce_value("", Optional[list], ["x"]) is None

    # Bare tuple
    assert worker._coerce_value("[10, 20]", tuple, ()) == (10, 20)
    assert worker._coerce_value("", tuple | None, (1,)) is None

    # Optional[bool] & bool | None empty strings
    assert worker._coerce_value("", Optional[bool], True) is None
    assert worker._coerce_value("   ", bool | None, False) is None
    assert worker._coerce_value("", bool, True) is True
    assert worker._coerce_value("true", bool | None, None) is True
    assert worker._coerce_value("0", bool | None, None) is False
    assert worker._coerce_value("yes", bool, False) is True


@default_preset("Custom Target", desc="Real-world target defaults")
@dataclass
class _WidgetParams:
    override_diameter: float | None = param(
        None,
        label="Diameter Override",
        unit="mm",
    )
    enabled: bool | None = param(None)
    tags: list = param_list(lambda: ["a", "b"])
    plain_list: list = field(default_factory=list)


def test_introspect_dataclass_with_pep604_and_default_preset() -> None:
    fields, defaults = worker._introspect_dataclass(_WidgetParams)
    by_name = {f["name"]: f for f in fields}

    assert by_name["override_diameter"]["type"] == "float"
    assert by_name["override_diameter"]["nullable"] is True
    assert by_name["override_diameter"]["unit"] == "mm"
    assert by_name["enabled"]["type"] == "bool"
    assert by_name["enabled"]["nullable"] is True
    assert by_name["tags"]["type"] == "list"
    assert by_name["tags"]["item"]["type"] == "str"
    assert defaults["tags"] == ["a", "b"]

    inst = worker._instantiate_dataclass(
        _WidgetParams,
        {
            "override_diameter": "12.5",
            "enabled": "",
            "plain_list": '["x", "y"]',
        },
    )
    assert inst.override_diameter == 12.5
    assert inst.enabled is None
    assert inst.plain_list == ["x", "y"]
    assert _WidgetParams.__default_preset__ == {
        "label": "Custom Target",
        "description": "Real-world target defaults",
    }


def test_multitype_nullable_unions_schema_and_coercion() -> None:
    str_float_opt = str | float | None
    unwrapped, is_nullable = worker._unwrap_optional(str_float_opt)
    assert is_nullable is True
    assert set(worker.get_args(unwrapped)) == {str, float}

    int_float_opt = int | float | None
    unwrapped_num, is_nullable_num = worker._unwrap_optional(int_float_opt)
    assert is_nullable_num is True
    assert set(worker.get_args(unwrapped_num)) == {int, float}

    # Coercion preserves matching member types and converts "" -> None when str not in union
    assert worker._coerce_value(None, str_float_opt, None) is None
    assert worker._coerce_value(12.5, str_float_opt, None) == 12.5
    assert isinstance(worker._coerce_value(12.5, str_float_opt, None), float)
    assert worker._coerce_value(12, str_float_opt, None) == 12.0
    assert worker._coerce_value("auto", str_float_opt, None) == "auto"
    assert worker._coerce_value("", str_float_opt, None) == ""

    assert worker._coerce_value("", int_float_opt, None) is None
    assert worker._coerce_value("   ", int_float_opt, None) is None
    assert worker._coerce_value(None, int_float_opt, None) is None
    assert worker._coerce_value(5, int_float_opt, None) == 5
    assert isinstance(worker._coerce_value(5, int_float_opt, None), int)
    assert worker._coerce_value("7", int_float_opt, None) == 7
    assert isinstance(worker._coerce_value("7", int_float_opt, None), int)
    assert worker._coerce_value("7.5", int_float_opt, None) == 7.5
    assert worker._coerce_value("", int | float, 2.0) == 2.0


@dataclass
class _MultiUnionParams:
    multi_str_float: str | float | None = None
    multi_int_float: int | float | None = None
    typed_default_float: str | float | None = 12.5


def test_introspect_multitype_nullable_unions() -> None:
    fields, defaults = worker._introspect_dataclass(_MultiUnionParams)
    by_name = {f["name"]: f for f in fields}

    assert by_name["multi_str_float"]["type"] == "str"
    assert by_name["multi_str_float"]["nullable"] is True
    assert by_name["multi_str_float"]["default"] is None
    assert defaults["multi_str_float"] is None

    assert by_name["multi_int_float"]["type"] == "float"
    assert by_name["multi_int_float"]["nullable"] is True
    assert by_name["multi_int_float"]["default"] is None
    assert defaults["multi_int_float"] is None

    assert by_name["typed_default_float"]["type"] == "float"
    assert by_name["typed_default_float"]["nullable"] is True
    assert by_name["typed_default_float"]["default"] == 12.5

    inst = worker._instantiate_dataclass(
        _MultiUnionParams,
        {"multi_str_float": 9.25, "multi_int_float": ""},
    )
    assert inst.multi_str_float == 9.25
    assert inst.multi_int_float is None


@dataclass
class _TypeCheckingBaseParams:
    inherited_opt: float | None = None


@dataclass
class _TypeCheckingSiblingParams(_TypeCheckingBaseParams):
    x: int | None = None
    tc: "OnlyTypeCheckingSymbol | None" = None  # type: ignore[name-defined]


def test_safe_get_type_hints_preserves_siblings_with_type_checking_field() -> None:
    fields, defaults = worker._introspect_dataclass(_TypeCheckingSiblingParams)
    by_name = {f["name"]: f for f in fields}

    assert by_name["x"]["type"] == "int"
    assert by_name["x"]["nullable"] is True
    assert by_name["x"]["default"] is None
    assert by_name["inherited_opt"]["type"] == "float"
    assert by_name["inherited_opt"]["nullable"] is True
    assert defaults["x"] is None

    inst = worker._instantiate_dataclass(_TypeCheckingSiblingParams, {"x": "42", "inherited_opt": ""})
    assert inst.x == 42
    assert inst.inherited_opt is None

    def _fn(a: int | None = None, tc: "OnlyTypeCheckingSymbol | None" = None) -> None:  # type: ignore[name-defined]
        return None

    fn_fields, _ = worker._introspect_function_signature(_fn)
    fn_by_name = {f["name"]: f for f in fn_fields}
    assert fn_by_name["a"]["type"] == "int"
    assert fn_by_name["a"]["nullable"] is True
    assert fn_by_name["a"]["default"] is None
